package tyr

import "fmt"

// MetaKey is a key for typed metadata of operations, such as the roles
// allowed to call them. Options made by [MetaKey.Option] set it when an
// operation is registered, and interceptors and transports read it with
// [MetaKey.From].
//
// A MetaKey is identified by its pointer: every call to [NewMetaKey]
// returns a distinct key, so keys from different packages never collide,
// even when they share a name and a type. Option and From panic on a nil
// *MetaKey.
type MetaKey[T any] struct {
	// name is only for String and panic messages. It also gives MetaKey a
	// non-zero size: pointers to distinct zero-size variables may compare
	// equal, pointers to distinct MetaKeys never do.
	name string
}

// NewMetaKey returns a new key for metadata of type T. Keys are usually
// declared once, as package-level variables. The name shows up only in
// [MetaKey.String] and panic messages and doesn't have to be unique.
func NewMetaKey[T any](name string) *MetaKey[T] {
	return &MetaKey[T]{name: name}
}

// Option returns an option that sets k to v for the operation it is
// registered with. When several options set the same key, the last one
// applied wins: an operation's own options apply after those of its
// groups, and a nested group's after those of the outer group.
//
// v is stored as is, not copied: a slice or map passed to Option must not
// be changed after the operation is registered. The option panics if it is
// applied to an operation that is already registered.
func (k *MetaKey[T]) Option(v T) OpOption {
	k.nilCheck()
	return func(op *Operation) {
		if op.registered {
			panic(fmt.Sprintf("tyr: MetaKey %q: option applied to registered operation %q", k.name, op.name))
		}
		if op.meta == nil {
			op.meta = make(map[any]any)
		}
		op.meta[k] = v
	}
}

// From returns the value an option of k set for op and reports whether
// such an option was applied. This differs from ctxkey, where a nil value
// of an interface type T is indistinguishable from a missing one: From
// reports true for a nil value that an option set. The value is shared by
// all calls of op and must not be changed.
func (k *MetaKey[T]) From(op *Operation) (T, bool) {
	k.nilCheck()
	v, ok := op.meta[k]
	if !ok {
		var zero T
		return zero, false
	}
	t, _ := v.(T) // a nil value of an interface type T doesn't assert, but it is set
	return t, true
}

// String returns the key's name.
func (k *MetaKey[T]) String() string {
	return k.name
}

// nilCheck panics if k is nil. Without it, a nil *MetaKey would be a valid
// map key, and all nil keys of one type would share a value.
func (k *MetaKey[T]) nilCheck() {
	if k == nil {
		panic("tyr: nil MetaKey")
	}
}
