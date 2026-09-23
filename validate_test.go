package tyr_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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

// tagged is a request with validate tags whose Validate records that it
// ran and relies on the tags: URL is set.
type tagged struct {
	URL  string `json:"url" validate:"required,url"`
	Code string `json:"code" validate:"omitempty,min=4"`
	ran  *bool
}

func (r tagged) Validate() error {
	*r.ran = true
	if strings.HasSuffix(r.URL, ".example") {
		return errors.New("example URLs aren't allowed")
	}
	return nil
}

func TestValidateTags(t *testing.T) {
	handled := false
	op := tyr.New().Handle("links.create", func(ctx context.Context, req tagged) (string, error) {
		handled = true
		return "ok", nil
	})

	t.Run("violations", func(t *testing.T) {
		ran := false
		handled = false
		_, err := op.Call(t.Context(), fill(tagged{Code: "ab", ran: &ran}))

		// In the order of the fields; neither Validate nor the handler runs.
		want := tyr.Violations{
			{Pointer: "/url", Detail: "is required"},
			{Pointer: "/code", Detail: "must be at least 4 characters"},
		}
		e, ok := err.(*tyr.Error)
		if !ok || e.Kind != tyr.KindInvalidArgument || e.Message != "validation failed" {
			t.Fatalf("Call() error = %v, want invalid_argument: validation failed", err)
		}
		if got, _ := e.Details.(tyr.Violations); !slices.Equal(got, want) {
			t.Errorf("Call() violations = %q, want %q", got, want)
		}
		if ran || handled {
			t.Errorf("Validate ran: %t, handler called: %t; want neither", ran, handled)
		}
	})
	t.Run("valid tags", func(t *testing.T) {
		ran := false
		handled = false
		_, err := op.Call(t.Context(), fill(tagged{URL: "https://links.example", ran: &ran}))
		if e, ok := err.(*tyr.Error); !ok || e.Message != "example URLs aren't allowed" || !ran || handled {
			t.Errorf("Call() error = %v, Validate ran: %t, handler called: %t; want Validate's error only", err, ran, handled)
		}
	})
}

func TestHandleBadValidateTag(t *testing.T) {
	got := panicValue(func() {
		tyr.New().Handle("links.get", func(ctx context.Context, req struct {
			Code string `json:"code" validate:"required,maxx=4"`
		}) (string, error) {
			return "", nil
		})
	})
	want := `tyr: Handle("links.get"): field Code: validate:"required,maxx=4": unknown rule "maxx"; ` +
		`add the validate/playground module for more rules, or move the check to Validate()`
	if got != want {
		t.Errorf("Handle() panicked with %v, want %q", got, want)
	}
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
