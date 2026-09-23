package tyr_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/iaxel/tyr"
)

// checked is a request whose Validate records that it ran and returns Err,
// or panics if Panic is set.
type checked struct {
	Err   error `json:"-"`
	Panic bool  `json:"-"`
	ran   *bool
}

func (r checked) Validate() error {
	if r.ran != nil {
		*r.ran = true
	}
	if r.Panic {
		panic("boom")
	}
	return r.Err
}

// checkedByPointer is a request whose Validate has a pointer receiver.
type checkedByPointer struct {
	ran *bool
}

func (r *checkedByPointer) Validate() error {
	*r.ran = true
	return nil
}

// fill returns a decode that fills in req, as a transport would.
func fill[Req any](req Req) func(dst any) error {
	return func(dst any) error {
		*dst.(*Req) = req
		return nil
	}
}

func TestValidate(t *testing.T) {
	var nilErr *tyr.Error
	plain := errors.New("end must be after start")
	expired := tyr.FailedPrecondition("link expired")
	var v tyr.Violations
	v.Add("code", "only a-z, 0-9 and '-'")
	violations := v.Err()

	tests := []struct {
		name     string
		req      checked
		wantKind tyr.Kind // of the call's error
		wantMsg  string   // of the call's error; "" if the call succeeds
		wantSame error    // an Error the call returns as is, if set
		wantIs   error    // an error the call's error wraps, if set
		wantText string   // the Error() of the call's error, if set
		wantLog  string   // the message of the one record logged, if any
	}{
		{name: "nil"},
		{
			name:     "violations",
			req:      checked{Err: violations},
			wantKind: tyr.KindInvalidArgument,
			wantMsg:  "validation failed",
			wantSame: violations,
		},
		{
			name:     "other Error",
			req:      checked{Err: expired},
			wantKind: tyr.KindFailedPrecondition,
			wantMsg:  "link expired",
			wantSame: expired,
		},
		{
			name:     "wrapped Error",
			req:      checked{Err: fmt.Errorf("check: %w", expired)},
			wantKind: tyr.KindFailedPrecondition,
			wantMsg:  "link expired",
			wantSame: expired,
		},
		{
			// Validate writes its errors for the client, so the text
			// becomes the message.
			name:     "plain error",
			req:      checked{Err: plain},
			wantKind: tyr.KindInvalidArgument,
			wantMsg:  "end must be after start",
			wantIs:   plain,
			wantText: "invalid_argument: end must be after start",
		},
		{
			name:     "nil *tyr.Error",
			req:      checked{Err: nilErr},
			wantKind: tyr.KindInternal,
			wantMsg:  "internal error",
			wantLog:  "tyr: operation failed",
		},
		{
			name:     "panic",
			req:      checked{Panic: true},
			wantKind: tyr.KindInternal,
			wantMsg:  "internal error",
			wantLog:  "tyr: panic",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			api := tyr.New(tyr.WithLogger(slog.New(rec)))
			mapped := false
			api.MapError(func(error) error {
				mapped = true
				return nil
			})
			handled := false
			op := api.Handle("links.check", func(ctx context.Context, req checked) (string, error) {
				handled = true
				return "ok", nil
			})

			res, err := op.Call(t.Context(), fill(tt.req))

			if tt.wantMsg == "" {
				if res != "ok" || err != nil || !handled {
					t.Errorf("Call() = %v, %v, handler called: %t; want ok, <nil>, true", res, err, handled)
				}
			} else {
				if e, ok := err.(*tyr.Error); !ok || e.Kind != tt.wantKind || e.Message != tt.wantMsg {
					t.Errorf("Call() error = %#v, want a *tyr.Error of kind %v with message %q", err, tt.wantKind, tt.wantMsg)
				}
				if tt.wantSame != nil && err != tt.wantSame {
					t.Errorf("Call() error = %v, want the Error from Validate as is", err)
				}
				if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
					t.Errorf("Call() error %v doesn't wrap %v", err, tt.wantIs)
				}
				if tt.wantText != "" && err.Error() != tt.wantText {
					t.Errorf("Call() error = %q, want %q", err.Error(), tt.wantText)
				}
				if handled {
					t.Error("the handler was called")
				}
			}
			if mapped {
				t.Error("a mapper got the error of Validate")
			}
			if tt.wantLog == "" && len(rec.logs) != 0 || tt.wantLog != "" && (len(rec.logs) != 1 || rec.logs[0].msg != tt.wantLog) {
				t.Errorf("logged %+v, want %q", rec.logs, tt.wantLog)
			}
		})
	}
}

func TestValidateReceivers(t *testing.T) {
	t.Run("value", func(t *testing.T) {
		ran := false
		op := tyr.New().Handle("links.check", func(ctx context.Context, req checked) (string, error) {
			return "ok", nil
		})
		if _, err := op.Call(t.Context(), fill(checked{ran: &ran})); err != nil || !ran {
			t.Errorf("Call() error = %v, Validate ran: %t; want <nil>, true", err, ran)
		}
	})
	t.Run("pointer", func(t *testing.T) {
		ran := false
		op := tyr.New().Handle("links.check", func(ctx context.Context, req checkedByPointer) (string, error) {
			return "ok", nil
		})
		if _, err := op.Call(t.Context(), fill(checkedByPointer{ran: &ran})); err != nil || !ran {
			t.Errorf("Call() error = %v, Validate ran: %t; want <nil>, true", err, ran)
		}
	})
}

func TestValidateAfterInterceptors(t *testing.T) {
	handler := func(ctx context.Context, req checked) (string, error) { return "ok", nil }

	t.Run("denied", func(t *testing.T) {
		api := tyr.New()
		api.Use(func(ctx context.Context, op *tyr.Operation, req any, next tyr.Invoker) (any, error) {
			return nil, tyr.Unauthenticated("log in first")
		})
		op := api.Handle("links.check", handler)

		// A client that isn't logged in learns that, not what's wrong with
		// its request.
		ran := false
		_, err := op.Call(t.Context(), fill(checked{Err: errors.New("bad code"), ran: &ran}))
		if e, ok := err.(*tyr.Error); !ok || e.Kind != tyr.KindUnauthenticated || ran {
			t.Errorf("Call() error = %v, Validate ran: %t; want unauthenticated, false", err, ran)
		}
	})
	t.Run("changed request", func(t *testing.T) {
		api := tyr.New()
		api.Use(func(ctx context.Context, op *tyr.Operation, req any, next tyr.Invoker) (any, error) {
			req.(*checked).Err = nil // fixes up the request before it's validated
			return next(ctx, req)
		})
		op := api.Handle("links.check", handler)

		if _, err := op.Call(t.Context(), fill(checked{Err: errors.New("bad code")})); err != nil {
			t.Errorf("Call() error = %v, want <nil>: Validate sees the changed request", err)
		}
	})
}
