// Package reqid holds the rule for request IDs, which
// middleware.RequestID takes from clients and the client of jsonrpc sends
// on, so that a service keeps the ID that another one sends it.
package reqid

// Header carries the ID of a request, in requests and in responses.
const Header = "X-Request-ID"

// Valid reports whether id is 1 to 128 characters of [A-Za-z0-9._:-]. An
// ID comes from a client and goes into every log record of its request,
// so it must be short and plain.
func Valid(id string) bool {
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
