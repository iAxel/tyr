package tyr_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/iaxel/tyr"
)

func ExampleOperation_Call() {
	type GetLinkReq struct {
		Code string `json:"code"`
	}
	errNotFound := errors.New("store: not found") // a domain error

	api := tyr.New()
	api.MapError(func(err error) error {
		if errors.Is(err, errNotFound) {
			return tyr.NotFound("link not found").WithCause(err)
		}
		return err
	})
	get := api.Handle("links.get", func(ctx context.Context, req GetLinkReq) (string, error) {
		if req.Code != "go" {
			return "", errNotFound
		}
		return "https://go.dev", nil
	})

	// Transports pass a decode for their wire format; this one fills in the
	// request directly, as a test would.
	for _, code := range []string{"go", "rust"} {
		url, err := get.Call(context.Background(), func(dst any) error {
			dst.(*GetLinkReq).Code = code
			return nil
		})
		fmt.Println(url, err)
	}
	// Output:
	// https://go.dev <nil>
	// <nil> not_found: link not found: store: not found
}

func ExampleAPI_Use() {
	type GetLinkReq struct {
		Code string `json:"code"`
	}

	// logCalls reports the outcome of every call; a real one would log it
	// or record a metric.
	logCalls := func(ctx context.Context, op *tyr.Operation, req any, next tyr.Invoker) (any, error) {
		res, err := next(ctx, req)
		kind := "ok"
		if e, ok := errors.AsType[*tyr.Error](err); ok {
			kind = e.Kind.String()
		}
		fmt.Println(op.Name(), kind)
		return res, err
	}

	api := tyr.New()
	api.Use(logCalls) // the first interceptor is the outermost
	get := api.Handle("links.get", func(ctx context.Context, req GetLinkReq) (string, error) {
		if req.Code != "go" {
			return "", tyr.NotFound("link not found")
		}
		return "https://go.dev", nil
	})
	api.Seal() // as a transport does when it mounts the API

	for _, code := range []string{"go", "rust"} {
		_, _ = get.Call(context.Background(), func(dst any) error {
			dst.(*GetLinkReq).Code = code
			return nil
		})
	}
	// Output:
	// links.get ok
	// links.get not_found
}

func ExampleError_WithCause() {
	errStore := errors.New("store: not found") // a domain error

	// E.g. in an error mapper: the client gets the kind and the message,
	// the logs get the cause too.
	err := tyr.NotFound("link not found").WithCause(errStore)

	fmt.Println(err.Kind, err.Message)
	fmt.Println(err)
	fmt.Println(errors.Is(err, errStore))
	// Output:
	// not_found link not found
	// not_found: link not found: store: not found
	// true
}

func ExampleError_Is() {
	// A shared error, e.g. a package-level variable.
	errExpired := tyr.FailedPrecondition("link expired")

	// Copies made by With* still match it, also when wrapped...
	err := fmt.Errorf("links.get: %w", errExpired.WithCause(errors.New("store: expired")))
	fmt.Println(errors.Is(err, errExpired))

	// ...but distinct errors don't, even with the same kind and message.
	fmt.Println(errors.Is(tyr.FailedPrecondition("link expired"), errExpired))
	// Output:
	// true
	// false
}
