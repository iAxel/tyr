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
