package tyr_test

import (
	"errors"
	"fmt"

	"github.com/iaxel/tyr"
)

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
