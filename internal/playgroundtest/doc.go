// Package playgroundtest compares the validate tags of the core with
// go-playground/validator v10.30.5, whose semantics the core promises, on
// the values of plantest.Checks. It is a module of its own, so that the
// root module has no dependencies, holds only tests and is never
// published. Run them from its directory:
//
//	cd internal/playgroundtest && go test ./...
package playgroundtest
