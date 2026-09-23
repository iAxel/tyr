// Package ctxkey provides typed context keys.
//
// A [Key] is identified by its pointer: every call to [New] returns a
// distinct key, so keys from different packages never collide, even when
// they share a name and a type.
package ctxkey

import (
	"context"
	"strconv"
)

// Key is a typed key for context values. Create keys with [New] and use
// them through the returned pointer. Methods panic on a nil *Key.
type Key[T any] struct {
	// name is only for String and panic messages. It also gives Key a
	// non-zero size: pointers to distinct zero-size variables may compare
	// equal, pointers to distinct Keys never do.
	name string
}

// New returns a new key for values of type T. Keys are usually declared
// once, as package-level variables. The name shows up only in [Key.String]
// and panic messages and doesn't have to be unique.
func New[T any](name string) *Key[T] {
	return &Key[T]{name: name}
}

// Set returns a derived context that carries v under k.
// Like [context.WithValue], it leaves ctx unchanged.
func (k *Key[T]) Set(ctx context.Context, v T) context.Context {
	k.nilCheck()
	return context.WithValue(ctx, k, v)
}

// Get returns the value ctx carries under k and reports whether it was
// found. If T is an interface type, a nil value is indistinguishable from
// a missing one, as with [context.Context.Value]: Get reports false for both.
func (k *Key[T]) Get(ctx context.Context) (T, bool) {
	k.nilCheck()
	v, ok := ctx.Value(k).(T)
	return v, ok
}

// MustGet is like [Key.Get] but panics if the value is not found.
// Use it only where the value is guaranteed, e.g. set by middleware
// that always runs first.
func (k *Key[T]) MustGet(ctx context.Context) T {
	v, ok := k.Get(ctx)
	if !ok {
		panic("ctxkey: no value for key " + strconv.Quote(k.name))
	}
	return v
}

// String returns the key's name, so that printing a context shows the
// name of the key rather than its type.
func (k *Key[T]) String() string {
	return k.name
}

// nilCheck panics if k is nil. context.WithValue's own nil-key check
// doesn't replace it: a typed nil pointer in an any is not equal to nil,
// so WithValue would accept a nil *Key and all nil keys of one type
// would share a value.
func (k *Key[T]) nilCheck() {
	if k == nil {
		panic("ctxkey: nil key")
	}
}
