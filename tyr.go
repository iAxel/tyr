// Package tyr provides typed operations and contracts: a handler is written
// once as a plain func(ctx, Req) (Res, error), served over REST and JSON-RPC
// and called from a typed client.
package tyr

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"reflect"
	"slices"
	"strings"
)

// Handler is the only shape business logic takes: a plain function of a
// context and a request, unaware of the transport that serves it.
type Handler[Req, Res any] = func(ctx context.Context, req Req) (Res, error)

// API is a set of operations. Create it with [New], register operations
// with [API.Handle] and mount it on transports, which [API.Seal] it.
//
// Configure an API from a single goroutine before serving it. Once sealed,
// it is read-only, and its operations may be called concurrently.
type API struct {
	ops     []*Operation
	names   map[string]bool
	mappers []func(error) error
	log     *slog.Logger
	sealed  bool
}

// Option configures an [API] created by [New].
type Option func(*API)

// WithLogger sets the logger for failed calls that need attention: internal
// errors and panics are logged at the error level, exceeded deadlines and
// unavailable services at the warning level, unless the caller canceled the
// call. Records are logged with the call's context, which carries the
// operation; see [OperationFrom]. By default, the API logs to
// [slog.Default] as it is at the time of logging.
func WithLogger(l *slog.Logger) Option {
	return func(a *API) { a.log = l }
}

// New returns an empty API configured by opts.
func New(opts ...Option) *API {
	a := &API{names: make(map[string]bool)}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// MapError adds a mapper that translates errors of other packages into an
// [Error], such as a store's ErrNotFound into [KindNotFound], so that those
// packages don't have to import tyr.
//
// A failed call's error reaches the mappers only if it doesn't contain an
// [Error] already. Each mapper gets that original error, in the order the
// mappers were added, and the first result that contains an [Error] is
// used; results without one, including nil, are ignored. See
// [Operation.Call] for what happens to errors no mapper translates.
//
// MapError panics if fn is nil or the API is sealed.
func (a *API) MapError(fn func(error) error) {
	a.checkOpen("MapError")
	if fn == nil {
		panic("tyr: MapError: nil mapper")
	}
	a.mappers = append(a.mappers, fn)
}

// Handle registers h as an operation with the given name and returns the
// operation. The name is one or more dot-separated segments of ASCII
// letters, digits, '_' and '-', such as "links.get"; the first segment
// can't be "rpc", which JSON-RPC reserves. Req must be a struct type.
//
// opts configure the operation, e.g. with a route for a transport, and
// apply in order. Handle panics if the name is invalid or already taken,
// h or an option is nil, Req isn't a struct, or the API is sealed.
func (a *API) Handle[Req, Res any](name string, h Handler[Req, Res], opts ...OpOption) *Operation {
	return register(a, name, h, nil, opts)
}

// Group returns a group whose operations get opts before their own
// options, e.g. to require a role for all of them.
func (a *API) Group(opts ...OpOption) *Group {
	return &Group{api: a, opts: slices.Clone(opts)}
}

// Operations returns the registered operations in the order of
// registration.
func (a *API) Operations() iter.Seq[*Operation] {
	return slices.Values(a.ops)
}

// Seal marks the API as complete: registering operations or error mappers
// after it panics. Transports seal the API when they mount it, so that an
// operation registered too late fails at startup instead of being silently
// left out. Sealing a sealed API does nothing.
func (a *API) Seal() {
	a.sealed = true
}

// checkOpen panics if a is sealed; call names the offending call.
func (a *API) checkOpen(call string) {
	if a.sealed {
		panic("tyr: " + call + " after Seal: register everything before mounting the API")
	}
}

// logger returns the logger set by WithLogger or the default one.
func (a *API) logger() *slog.Logger {
	if a.log != nil {
		return a.log
	}
	return slog.Default()
}

// Group registers operations in an [API] with shared options; see
// [API.Group].
type Group struct {
	api  *API
	opts []OpOption
}

// Handle is like [API.Handle] but applies the group's options before opts.
func (g *Group) Handle[Req, Res any](name string, h Handler[Req, Res], opts ...OpOption) *Operation {
	return register(g.api, name, h, g.opts, opts)
}

// Group returns a nested group, whose options go after g's.
func (g *Group) Group(opts ...OpOption) *Group {
	return &Group{api: g.api, opts: slices.Concat(g.opts, opts)}
}

// register implements API.Handle and Group.Handle.
func register[Req, Res any](a *API, name string, h Handler[Req, Res], groupOpts, opts []OpOption) *Operation {
	call := fmt.Sprintf("Handle(%q)", name)
	a.checkOpen(call)
	if problem := checkName(name); problem != "" {
		panic("tyr: " + call + ": " + problem)
	}
	if a.names[name] {
		panic("tyr: " + call + ": duplicate operation name")
	}
	if h == nil {
		panic("tyr: " + call + ": nil handler")
	}
	if t := reflect.TypeFor[Req](); t.Kind() != reflect.Struct {
		msg := fmt.Sprintf("tyr: %s: request type %v is not a struct", call, t)
		if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
			msg += fmt.Sprintf("; use %v", t.Elem())
		}
		panic(msg)
	}

	op := newOperation(a, name, h)
	for _, opt := range slices.Concat(groupOpts, opts) {
		if opt == nil {
			panic("tyr: " + call + ": nil option")
		}
		opt(op)
	}
	a.names[name] = true
	a.ops = append(a.ops, op)
	return op
}

// checkName reports what's wrong with name as an operation name, or "" if
// nothing is.
func checkName(name string) string {
	if first, _, _ := strings.Cut(name, "."); first == "rpc" {
		return `the first segment "rpc" is reserved by JSON-RPC`
	}
	for seg := range strings.SplitSeq(name, ".") {
		if seg == "" || strings.ContainsFunc(seg, notNameRune) {
			return "name must be dot-separated segments of ASCII letters, digits, '_' and '-'"
		}
	}
	return ""
}

// notNameRune reports whether r can't appear in a segment of an operation
// name.
func notNameRune(r rune) bool {
	switch {
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9', r == '_', r == '-':
		return false
	}
	return true
}
