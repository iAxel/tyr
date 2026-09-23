package middleware

import (
	"io"
	"log/slog"
	"maps"
	"net/http"
	"runtime/debug"
)

// internalError is the problem that Recover sends.
const internalError = `{"type":"about:blank","title":"Internal Server Error","status":500}`

// Recover returns a middleware that recovers a panic of the handler and
// logs it to l at Error: "middleware: panic" with the panic and the stack.
// A nil l means [slog.Default] as it is at the time of writing.
//
// If the response hasn't started, the client gets 500 as
// application/problem+json (RFC 9457). The headers go back to what they
// were before the handler: the ones it set were meant for the response it
// failed to make, like Content-Length or Set-Cookie. If the response has
// started, by a status other than 1xx or by 101 Switching Protocols, a
// write, a flush or the hijacking of the connection, Recover breaks the
// connection instead, by panicking with [http.ErrAbortHandler], which
// [http.Server] doesn't log.
//
// A panic with http.ErrAbortHandler itself goes through Recover, unlogged.
func Recover(l *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var header http.Header // as it is before the handler
			if h := w.Header(); len(h) > 0 {
				header = h.Clone()
			}
			ww, state := wrap(w)
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler {
					panic(v)
				}
				logger(l).ErrorContext(r.Context(), "middleware: panic", "panic", v, "stack", string(debug.Stack()))
				if state.started() {
					panic(http.ErrAbortHandler)
				}
				h := w.Header()
				clear(h)
				maps.Copy(h, header)
				h.Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, internalError)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
