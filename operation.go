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
	name string
	req  reflect.Type
	res  reflect.Type
	api  *API
	call func(ctx context.Context, decode func(dst any) error) (any, error)
}

// newOperation returns an operation of a that decodes a request and passes
// it to h.
func newOperation[Req, Res any](a *API, name string, h Handler[Req, Res]) *Operation {
	return &Operation{
		name: name,
		req:  reflect.TypeFor[Req](),
		res:  reflect.TypeFor[Res](),
		api:  a,
		call: func(ctx context.Context, decode func(dst any) error) (any, error) {
			req := new(Req)
			if decode != nil {
				if err := decode(req); err != nil {
					if _, ok := errors.AsType[*Error](err); !ok {
						err = InvalidArgument("invalid request").WithCause(err)
					}
					return nil, err
				}
			}
			return h(ctx, *req)
		},
	}
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

// Call runs the operation once: it decodes a request, calls the handler
// and returns its result. Transports call it for every request they serve,
// and tests may call it directly.
//
// decode fills in a new request, which it gets as a *Req; a nil decode
// leaves the request zero. Decode errors that don't contain an [Error] are
// reported as [KindInvalidArgument]. The handler's context carries the
// operation; see [OperationFrom].
//
// The error Call returns is either nil or an *Error. An error that contains
// an Error is reduced to it; others go through the mappers added by
// [API.MapError], and those no mapper translates become
// [KindDeadlineExceeded] if they are a [context.DeadlineExceeded] and
// [KindInternal] with a generic message otherwise. A panic becomes
// [KindInternal] too, except for [http.ErrAbortHandler], which Call panics
// with again. Failed calls that need attention are logged; see
// [WithLogger].
func (op *Operation) Call(ctx context.Context, decode func(dst any) error) (res any, err error) {
	ctx = operationKey.Set(ctx, op)
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(v)
			}
			op.api.logger().ErrorContext(ctx, "tyr: panic", "panic", v, "stack", string(debug.Stack()))
			res, err = nil, Internal("internal error").WithCause(panicError(v))
		}
	}()

	res, err = op.call(ctx, decode)
	if err != nil {
		e := op.api.resolve(err)
		op.api.logFailure(ctx, e)
		return nil, e
	}
	return res, nil
}

// resolve turns the error of a failed call into an Error, as described at
// Operation.Call.
func (a *API) resolve(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	for _, m := range a.mappers {
		if e, ok := errors.AsType[*Error](m(err)); ok {
			return e
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return DeadlineExceeded("deadline exceeded").WithCause(err)
	}
	return Internal("internal error").WithCause(err)
}

// logFailure logs the error of a failed call if it needs attention, as
// described at WithLogger.
func (a *API) logFailure(ctx context.Context, e *Error) {
	if errors.Is(e, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
		return // the caller has gone; nothing failed on our side
	}
	switch e.Kind {
	case KindInternal:
		a.logger().ErrorContext(ctx, "tyr: operation failed", "err", e)
	case KindDeadlineExceeded, KindUnavailable:
		a.logger().WarnContext(ctx, "tyr: operation failed", "err", e)
	}
}

// panicError turns a value recovered from a panic into an error, keeping
// the value's error chain if it has one.
func panicError(v any) error {
	if err, ok := v.(error); ok {
		return fmt.Errorf("panic: %w", err)
	}
	return fmt.Errorf("panic: %v", v)
}
