package middleware

import (
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Logger returns a middleware that writes a record to l at Info for every
// request once its handler is done: "middleware: request" with the method,
// the route (the pattern of the [http.ServeMux] that matched the request,
// empty if none did), the status and the duration. The request ID gets
// there from the request context, given a [tyr.NewLogHandler]. A nil l
// means [slog.Default] as it is at the time of writing.
//
// The status is the one the handler sent, or 200 if it sent none, as
// net/http then does. A handler that took over the connection (see
// [http.Hijacker]) gets hijacked=true instead, and a status only if it sent
// one before. A handler that didn't return, because a panic went through
// Logger, as when [Recover] breaks the connection, gets aborted=true and the
// status sent so far, 0 if none.
//
// The mux sets the route in the request it gets, and Logger reads it from
// the request it passes on, once the handler is done. A middleware between
// them that passes on another request, as [http.Request.WithContext]
// makes, hides the route from Logger. So when a request succeeds, with a
// 2xx or 3xx status, without a route, Logger warns once, at the first such
// request: "middleware: request without a route", with a hint. Requests
// that a mux or a middleware below Logger rejects have no route anyway.
func Logger(l *slog.Logger) func(http.Handler) http.Handler {
	var warned sync.Once // of a request without a route
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww, state := wrap(w)
			returned := false
			defer func() {
				attrs := make([]slog.Attr, 0, 6)
				attrs = append(attrs, slog.String("method", r.Method), slog.String("route", r.Pattern))
				status := state.status
				if status == 0 && returned && !state.hijacked {
					status = http.StatusOK
				}
				if status != 0 || !state.hijacked {
					attrs = append(attrs, slog.Int("status", status))
				}
				attrs = append(attrs, slog.Duration("duration", time.Since(start)))
				if state.hijacked {
					attrs = append(attrs, slog.Bool("hijacked", true))
				}
				if !returned {
					attrs = append(attrs, slog.Bool("aborted", true))
				}
				logger(l).LogAttrs(r.Context(), slog.LevelInfo, "middleware: request", attrs...)
				if returned && !state.hijacked && r.Pattern == "" && status >= 200 && status < 400 {
					warned.Do(func() {
						logger(l).WarnContext(r.Context(), "middleware: request without a route", "hint",
							"a middleware between Logger and the ServeMux passes on another request, "+
								"as r.WithContext makes, and hides the route: put it above Logger")
					})
				}
			}()
			next.ServeHTTP(ww, r)
			returned = true
		})
	}
}
