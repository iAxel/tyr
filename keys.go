package tyr

import (
	"context"

	"github.com/iaxel/tyr/ctxkey"
)

// RequestIDKey carries the ID of the request a context belongs to.
// Middleware of a transport sets it, and [NewLogHandler] adds it to log
// records.
var RequestIDKey = ctxkey.New[string]("request_id")

// operationKey carries the operation a call runs; see OperationFrom.
var operationKey = ctxkey.New[*Operation]("operation")

// OperationFrom returns the operation whose call ctx belongs to.
// [Operation.Call] puts it in the context it passes to the handler.
func OperationFrom(ctx context.Context) (*Operation, bool) {
	return operationKey.Get(ctx)
}

// WithOperation returns a derived context that carries op, as
// [OperationFrom] reads it. [Operation.Call] puts its operation in the
// context itself, unless the context already carries it. A transport uses
// WithOperation to keep the operation in the context beyond the call, e.g.
// to log a failure to encode the result with its operation.
//
// WithOperation panics if op is nil.
func WithOperation(ctx context.Context, op *Operation) context.Context {
	if op == nil {
		panic("tyr: WithOperation: nil operation")
	}
	return operationKey.Set(ctx, op)
}
