package tyr_test

import (
	"context"
	"testing"

	"github.com/iaxel/tyr"
)

func TestRequestID(t *testing.T) {
	if id, ok := tyr.RequestIDFrom(t.Context()); id != "" || ok {
		t.Errorf("RequestIDFrom(context without an ID) = %q, %t; want \"\", false", id, ok)
	}
	ctx := tyr.WithRequestID(t.Context(), "req-1")
	if id, ok := tyr.RequestIDFrom(ctx); id != "req-1" || !ok {
		t.Errorf("RequestIDFrom(WithRequestID(ctx, %q)) = %q, %t; want %[1]q, true", "req-1", id, ok)
	}
}

func TestOperationFrom(t *testing.T) {
	if op, ok := tyr.OperationFrom(t.Context()); op != nil || ok {
		t.Errorf("OperationFrom(context without an operation) = %v, %t; want <nil>, false", op, ok)
	}

	var got *tyr.Operation
	op := tyr.New().Handle("links.get", func(ctx context.Context, req getLinkReq) (*link, error) {
		got, _ = tyr.OperationFrom(ctx)
		return nil, nil
	})
	if _, err := op.Call(t.Context(), nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got != op {
		t.Errorf("OperationFrom(handler context) = %v, want the operation %q", got, op.Name())
	}
}

func TestWithOperation(t *testing.T) {
	op := tyr.New().Handle("links.get", getLink)
	if got, ok := tyr.OperationFrom(tyr.WithOperation(t.Context(), op)); got != op || !ok {
		t.Errorf("OperationFrom(WithOperation(ctx, op)) = %v, %t; want the operation, true", got, ok)
	}
	if got, want := panicValue(func() { tyr.WithOperation(t.Context(), nil) }), "tyr: WithOperation: nil operation"; got != want {
		t.Errorf("WithOperation(ctx, nil) panicked with %v, want %q", got, want)
	}
}

func TestCallContext(t *testing.T) {
	var got context.Context // what the handler got
	record := func(ctx context.Context, req getLinkReq) (*link, error) {
		got = ctx
		return nil, nil
	}
	api := tyr.New()
	get := api.Handle("links.get", record)
	list := api.Handle("links.list", record)

	// A context that carries the operation already is passed on as is.
	ctx := tyr.WithOperation(t.Context(), get)
	if _, err := get.Call(ctx, nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got != ctx {
		t.Error("Call() added the operation to a context that carries it already")
	}

	// A context of another operation gets this one.
	if _, err := list.Call(ctx, nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if op, _ := tyr.OperationFrom(got); op != list {
		t.Error("the handler's context doesn't carry links.list")
	}
}
