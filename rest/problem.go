package rest

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/iaxel/tyr"
)

// problem is a problem details object of RFC 9457 with the members of tyr.
type problem struct {
	Type    string         `json:"type"`
	Title   string         `json:"title"`
	Status  int            `json:"status"`
	Detail  string         `json:"detail,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Errors  tyr.Violations `json:"errors,omitempty"`
	Details any            `json:"details,omitempty"`
}

// WriteError writes err as REST writes the error of an operation: as
// application/problem+json with the status of its kind, for handlers
// outside operations. An err that contains a [tyr.Error] is written as is.
// Otherwise, as [tyr.Operation.Call] does for errors no mapper translates,
// a [context.DeadlineExceeded] becomes [tyr.KindDeadlineExceeded], and any
// other error, a nil *tyr.Error too, [tyr.KindInternal] with a generic
// message, and is logged to [slog.Default] with the context of r.
// WriteError doesn't add the WWW-Authenticate of [Challenge] to a 401.
// It panics if err is nil.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		panic("rest: WriteError: nil error")
	}
	ctx := r.Context()
	e, ok := errors.AsType[*tyr.Error](err)
	switch {
	case ok && e != nil:
	case ok: // a nil *tyr.Error, returned as an error by mistake
		slog.Default().ErrorContext(ctx, "rest: internal error", "err", "a nil *tyr.Error was written as an error")
		e = tyr.Internal("internal error")
	case errors.Is(err, context.DeadlineExceeded):
		e = tyr.DeadlineExceeded("deadline exceeded").WithCause(err)
	default:
		slog.Default().ErrorContext(ctx, "rest: internal error", "err", err)
		e = tyr.Internal("internal error").WithCause(err)
	}
	writeError(ctx, slog.Default(), w, e, nil)
}

// WriteProblem writes a problem of the HTTP request itself, as
// application/problem+json without a kind, like the 413 and 415 of
// [Mount]: {"type":"about:blank","title":"Forbidden","status":403}.
// It panics unless status is a 4xx or 5xx one.
func WriteProblem(w http.ResponseWriter, status int) {
	if status < 400 || status > 599 {
		panic(fmt.Sprintf("rest: WriteProblem(%d): want a 4xx or 5xx status", status))
	}
	writeProblem(context.Background(), slog.Default(), w, problem{Status: status})
}

// writeError sends e as a problem, or an internal error if e is nil. A 401
// gets a WWW-Authenticate header per challenge. logger logs details that
// can't be encoded.
func writeError(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, e *tyr.Error, challenges []string) {
	if e == nil {
		e = tyr.Internal("internal error")
	}
	p := problem{Status: statusOf(e.Kind), Detail: e.Message, Kind: e.Kind.String()}
	if e.Kind != tyr.KindInternal { // as in JSON-RPC, internal errors have no details
		if v, ok := e.Details.(tyr.Violations); ok {
			p.Errors = v
		} else {
			p.Details = e.Details
		}
	}
	if p.Status == http.StatusUnauthorized {
		for _, c := range challenges {
			w.Header().Add("WWW-Authenticate", c)
		}
	}
	writeProblem(ctx, logger, w, p)
}

// statusOf returns the HTTP status of errors of kind k.
func statusOf(k tyr.Kind) int {
	switch k {
	case tyr.KindInvalidArgument:
		return http.StatusBadRequest
	case tyr.KindUnauthenticated:
		return http.StatusUnauthorized
	case tyr.KindPermissionDenied:
		return http.StatusForbidden
	case tyr.KindNotFound:
		return http.StatusNotFound
	case tyr.KindAlreadyExists, tyr.KindFailedPrecondition:
		return http.StatusConflict
	case tyr.KindResourceExhausted:
		return http.StatusTooManyRequests
	case tyr.KindUnavailable:
		return http.StatusServiceUnavailable
	case tyr.KindDeadlineExceeded:
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

// writeProblem sends p as application/problem+json, with the type and the
// title filled in. Details that can't be encoded are a bug of the server:
// they're logged to logger and left out.
func writeProblem(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, p problem) {
	p.Type, p.Title = "about:blank", http.StatusText(p.Status)
	data, err := json.Marshal(p)
	if err != nil {
		// Only details can fail to encode: send the problem without them.
		logger.ErrorContext(ctx, "rest: encoding error details", "err", err)
		p.Errors, p.Details = nil, nil
		data, _ = json.Marshal(p)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_, _ = w.Write(data)
}
