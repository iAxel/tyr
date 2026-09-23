// Package rest serves the operations of a [tyr.API] over REST: [Mount]
// registers a handler on an [http.ServeMux] for every operation with a
// [Route].
//
//	api.Handle("links.get", links.Get, rest.Route("GET /links/{code}"))
//	rest.Mount(mux, api)
//
// # Requests
//
// A request becomes the operation's Req in two steps. First the JSON body,
// if there is one, is decoded into it. A body needs a JSON Content-Type,
// application/json or a +json type, or the response is 415 Unsupported
// Media Type; a body over the limit, 1 MiB by default (see [MaxBodyBytes]),
// gets 413. Then the fields tagged path, query or header get the value of
// that path wildcard, query parameter or header, over what the body set:
//
//	type ListReq struct {
//		Owner string   `json:"owner" path:"owner"`
//		Tags  []string `json:"tags" query:"tag"`
//	}
//
// Such fields may be strings, bools, integers and floats, or implement
// encoding.TextUnmarshaler, like time.Time does in RFC 3339, or be pointers
// to those, which get a value only when there is one; query fields may also
// be slices of those. A value that doesn't fit fails the call
// with [tyr.KindInvalidArgument] and a [tyr.Violation]: its pointer is the
// JSON name of the field, and its detail names the source, e.g.
// query parameter "tag": must be an integer.
//
// # Responses
//
// A result is sent as JSON with status 200 or the one set by [Status]. A
// result of an empty struct type has no body, and its status is 204 by
// default.
//
// An error is sent as application/problem+json (RFC 9457) with the status
// of its kind:
//
//   - invalid_argument: 400
//   - unauthenticated: 401
//   - permission_denied: 403
//   - not_found: 404
//   - already_exists, failed_precondition: 409
//   - resource_exhausted: 429
//   - unavailable: 503
//   - deadline_exceeded: 504
//   - internal and unknown kinds: 500
//
// The problem's detail is the error's message and its kind is the kind's
// name. [tyr.Violations] go in its errors member, other details in its
// details member; internal errors have neither. 413 and 415 have no kind:
// they are about the HTTP request, and the operation isn't called.
//
// A result or details that can't be encoded are a bug of the server: they
// are logged with [tyr.API.Logger], with the request's context, which also
// carries the operation (see [tyr.WithOperation]), and the client gets an
// internal error or the problem without its details.
package rest

import (
	"fmt"
	"net/http"

	"github.com/iaxel/tyr"
)

// The keys of the options; RouteOf reads the route.
var (
	routeKey  = tyr.NewMetaKey[string]("rest.route")
	statusKey = tyr.NewMetaKey[int]("rest.status")
	limitKey  = tyr.NewMetaKey[int64]("rest.max_body_bytes")
)

// defaultLimit is the size limit of request bodies without MaxBodyBytes.
const defaultLimit = 1 << 20

// Route serves an operation at pattern, in the syntax of [http.ServeMux]
// with a method: "GET /links/{code}". Every wildcard of the pattern needs a
// field of the request with a path tag of the same name, and every such
// field needs a wildcard. [Mount] checks that, and ServeMux checks the
// syntax.
func Route(pattern string) tyr.OpOption {
	return routeKey.Option(pattern)
}

// RouteOf returns the pattern that [Route] set for op and reports whether
// op has one.
func RouteOf(op *tyr.Operation) (pattern string, ok bool) {
	return routeKey.From(op)
}

// Status sets the status of a successful response, which is 200 by default
// and 204 for a result of an empty struct type. Status panics if code isn't
// a 2xx status, and [Mount] panics on 204 or 205 for a result with a body.
func Status(code int) tyr.OpOption {
	if code < 200 || code > 299 {
		panic(fmt.Sprintf("rest: Status(%d): want a 2xx status", code))
	}
	return statusKey.Option(code)
}

// MaxBodyBytes limits the size of request bodies to n bytes, 1 MiB by
// default; a larger body gets 413 Request Entity Too Large. Give it to a
// group to set the limit of several operations. MaxBodyBytes panics if n
// isn't positive.
func MaxBodyBytes(n int64) tyr.OpOption {
	if n <= 0 {
		panic(fmt.Sprintf("rest: MaxBodyBytes(%d): want a positive size", n))
	}
	return limitKey.Option(n)
}

// Mount seals api and registers a handler on mux for every operation that
// has a [Route]; operations without one are left out.
//
// Mount panics if a pattern has no method, ServeMux rejects a pattern or
// finds that it conflicts with another, a wildcard has no path field or a
// path field has no wildcard, a bound field can't be bound, or [Status]
// sets 204 or 205 for a result with a body. It also panics if mux or api
// is nil.
func Mount(mux *http.ServeMux, api *tyr.API) {
	if mux == nil || api == nil {
		panic("rest: Mount: nil mux or API")
	}
	api.Seal()
	for op := range api.Operations() {
		pattern, ok := RouteOf(op)
		if !ok {
			continue
		}
		handle(mux, op, pattern, newHandler(api, op, pattern))
	}
}
