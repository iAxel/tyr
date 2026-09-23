package tyr

import (
	"context"
	"slices"
)

// An Interceptor runs around the handler of every operation, e.g. to
// authorize, measure or trace calls; see [API.Use]. It gets the operation,
// to look at its metadata, and the request as a *Req, which it may change,
// or replace by passing another *Req to next. Passing next anything but a
// non-nil *Req fails the call with [KindInternal].
//
// An interceptor may also return without calling next, e.g. with a cached
// result. A result must be of the operation's Res type, or nil if Res can
// be nil, as a pointer can: [Operation.Call] fails with KindInternal
// rather than pass anything else on to a transport.
type Interceptor func(ctx context.Context, op *Operation, req any, next Invoker) (any, error)

// An Invoker runs the rest of a call: the interceptors after the current
// one and the handler. Its error is nil or an *Error: errors below it are
// already resolved as described at [Operation.Call], and panics below it
// are already recovered as [KindInternal], so an interceptor can look at
// the Kind, e.g. with [errors.AsType].
type Invoker func(ctx context.Context, req any) (any, error)

// chain returns the Invoker that runs op's handler through the API's
// interceptors, the first one outermost.
func (a *API) chain(op *Operation) Invoker {
	invoke := op.handle
	for _, ic := range slices.Backward(a.interceptors) {
		invoke = a.link(ic, op, invoke)
	}
	return invoke
}

// link returns the Invoker that runs ic around next, resolving the errors
// and recovering the panics of ic.
func (a *API) link(ic Interceptor, op *Operation, next Invoker) Invoker {
	return func(ctx context.Context, req any) (res any, err error) {
		defer a.recoverCall(ctx, &res, &err)
		if res, err = ic(ctx, op, req, next); err != nil {
			return nil, a.resolve(err)
		}
		return res, nil
	}
}
