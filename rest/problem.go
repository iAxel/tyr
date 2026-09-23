package rest

import (
	"context"
	"encoding/json/v2"
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

// writeError sends err, which Operation.Call returned, as a problem.
func (h *handler) writeError(ctx context.Context, w http.ResponseWriter, err error) {
	e, ok := err.(*tyr.Error)
	if !ok {
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
	h.writeProblem(ctx, w, p)
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
// they're logged and left out.
func (h *handler) writeProblem(ctx context.Context, w http.ResponseWriter, p problem) {
	p.Type, p.Title = "about:blank", http.StatusText(p.Status)
	data, err := json.Marshal(p)
	if err != nil {
		// Only details can fail to encode: send the problem without them.
		h.api.Logger().ErrorContext(ctx, "rest: encoding error details", "err", err)
		p.Errors, p.Details = nil, nil
		data, _ = json.Marshal(p)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_, _ = w.Write(data)
}
