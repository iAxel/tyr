package tyr_test

import (
	"context"
	"testing"

	"github.com/iaxel/tyr"
)

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
