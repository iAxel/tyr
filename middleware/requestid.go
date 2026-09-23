package middleware

import (
	"net/http"
	"uuid"

	"github.com/iaxel/tyr"
)

// requestIDHeader carries the ID of a request, both ways.
const requestIDHeader = "X-Request-ID"

// RequestID returns a middleware that gives every request an ID. It keeps
// the X-Request-ID of the request if it is 1 to 128 characters of
// [A-Za-z0-9._:-], and makes a new UUIDv7, which sorts by time, otherwise:
// the header comes from the client, and the ID goes into every log record
// of the request. The ID goes into the X-Request-ID header of the
// response, before the handler runs, and into the request context with
// [tyr.WithRequestID], where [tyr.RequestIDFrom] and [tyr.NewLogHandler]
// find it.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if !validID(id) {
				id = uuid.NewV7().String()
			}
			w.Header().Set(requestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(tyr.WithRequestID(r.Context(), id)))
		})
	}
}

// validID reports whether id is 1 to 128 characters of [A-Za-z0-9._:-].
func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := range len(id) {
		switch c := id[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}
