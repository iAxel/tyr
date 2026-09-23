package tyr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"runtime/debug"
)

// OpOption configures an operation when it is registered, e.g. with the
// route a transport serves it at; see [API.Handle].
type OpOption func(*Operation)

// Operation is an operation registered in an [API]. Transports serve it by
// its name, and interceptors get it to look at its metadata. An operation
// is read-only once registered.
type Operation struct {
	name       string
	req        reflect.Type
	res        reflect.Type
	api        *API
	meta       map[any]any // set by MetaKey options, keyed by *MetaKey
	registered bool        // once set, the operation is read-only

	decode func(decode func(dst any) error) (any, error) // returns a new *Req
	handle Invoker                                       // the innermost link: checks the *Req, calls the handler
	invoke Invoker                                       // the whole chain, built by API.Seal
}

// newOperation returns an operation of a that passes requests to h.
func newOperation[Req, Res any](a *API, name string, h Handler[Req, Res]) *Operation {
	_, validates := any((*Req)(nil)).(Validator)
	return &Operation{
		name: name,
		req:  reflect.TypeFor[Req](),
		res:  reflect.TypeFor[Res](),
		api:  a,
		decode: func(decode func(dst any) error) (any, error) {
			req := new(Req)
			if decode != nil {
				if err := decode(req); err != nil {
					if _, ok := errors.AsType[*Error](err); !ok {
						err = InvalidArgument("invalid request").WithCause(err)
					}
					return nil, err
				}
			}
			return req, nil
		},
		handle: func(ctx context.Context, req any) (res any, err error) {
			defer a.recoverCall(ctx, &res, &err)
			r, ok := req.(*Req)
			if !ok || r == nil {
				return nil, Internal("internal error").WithCause(wrongRequest[Req](req))
			}
			if validates {
				if err := any(r).(Validator).Validate(); err != nil {
					return nil, a.validationError(err)
				}
			}
			if res, err = h(ctx, *r); err != nil {
				return nil, a.resolve(err)
			}
			return res, nil
		},
	}
}

// wrongRequest describes what an interceptor passed to next instead of a
// non-nil *Req.
func wrongRequest[Req any](req any) error {
	want := reflect.TypeFor[*Req]()
	if reflect.TypeOf(req) == want {
		return fmt.Errorf("tyr: an interceptor passed a nil %v to next", want)
	}
	return fmt.Errorf("tyr: an interceptor passed %T to next, want %v", req, want)
}

// Name returns the name the operation is registered under.
func (op *Operation) Name() string {
	return op.name
}

// Req returns the type of the operation's request.
func (op *Operation) Req() reflect.Type {
	return op.req
}

// Res returns the type of the operation's result.
func (op *Operation) Res() reflect.Type {
	return op.res
}

// Call runs the operation once: it decodes a request, passes it through
// the interceptors, validates it (see [Validator]), calls the handler and
// returns the result. Transports call it for every request they serve, and
// tests may call it directly.
//
// decode fills in a new request, which it gets as a *Req; a nil decode
// leaves the request zero. Decode errors that don't contain an [Error] are
// reported as [KindInvalidArgument]. The contexts of the interceptors and
// the handler carry the operation; Call adds it to ctx unless ctx already
// carries it, see [WithOperation] and [OperationFrom].
//
// The error Call returns is either nil or an *Error. An error that contains
// an Error is reduced to it; others go through the mappers added by
// [API.MapError], and those no mapper translates become
// [KindDeadlineExceeded] if they are a [context.DeadlineExceeded] and
// [KindInternal] with a generic message otherwise. A nil *Error returned as
// a non-nil error becomes [KindInternal] too. So does a panic, except for
// [http.ErrAbortHandler], which Call panics with again. Failed calls that
// need attention are logged; see [WithLogger].
func (op *Operation) Call(ctx context.Context, decode func(dst any) error) (any, error) {
	if cur, ok := OperationFrom(ctx); !ok || cur != op {
		ctx = WithOperation(ctx, op)
	}
	res, err := op.run(ctx, decode)
	if err != nil {
		e := op.api.resolve(err)
		op.api.logFailure(ctx, e)
		return nil, e
	}
	return res, nil
}

// run decodes a request and passes it through the chain.
func (op *Operation) run(ctx context.Context, decode func(dst any) error) (res any, err error) {
	defer op.api.recoverCall(ctx, &res, &err)
	req, err := op.decode(decode)
	if err != nil {
		return nil, err
	}
	invoke := op.invoke
	if invoke == nil {
		invoke = op.api.chain(op) // not sealed yet: interceptors may still be added
	}
	return invoke(ctx, req)
}

// resolve turns the error of a failed call into an Error, as described at
// Operation.Call.
func (a *API) resolve(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		if e == nil {
			return Internal("internal error").WithCause(errors.New("tyr: a nil *tyr.Error was returned as an error"))
		}
		return e
	}
	for _, m := range a.mappers {
		if e, ok := errors.AsType[*Error](m(err)); ok && e != nil {
			return e
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return DeadlineExceeded("deadline exceeded").WithCause(err)
	}
	return Internal("internal error").WithCause(err)
}

// recoverCall is deferred by every link of a call. It turns a panic into an
// internal error in *err and logs the panic with its stack right away, so
// that it stays in the logs even if an interceptor turns the error into a
// success. It panics again with http.ErrAbortHandler.
func (a *API) recoverCall(ctx context.Context, res *any, err *error) {
	v := recover()
	if v == nil {
		return
	}
	if v == http.ErrAbortHandler {
		panic(v)
	}
	a.Logger().ErrorContext(ctx, "tyr: panic", "panic", v, "stack", string(debug.Stack()))
	*res, *err = nil, Internal("internal error").WithCause(&panicError{value: v})
}

// panicError is the cause of the error a recovered panic becomes.
type panicError struct {
	value any
}

func (p *panicError) Error() string {
	return fmt.Sprintf("panic: %v", p.value)
}

// Unwrap returns the panic value if it is an error.
func (p *panicError) Unwrap() error {
	err, _ := p.value.(error)
	return err
}

// logFailure logs the final error of a call if it needs attention, as
// described at WithLogger.
func (a *API) logFailure(ctx context.Context, e *Error) {
	if _, ok := errors.AsType[*panicError](e); ok {
		return // logged where it was recovered
	}
	if errors.Is(e, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
		return // the caller has gone; nothing failed on our side
	}
	switch e.Kind {
	case KindInternal:
		a.Logger().ErrorContext(ctx, "tyr: operation failed", "err", e)
	case KindDeadlineExceeded, KindUnavailable:
		a.Logger().WarnContext(ctx, "tyr: operation failed", "err", e)
	}
}
