// Package plan builds the per-type plans that are made once, when an API is
// set up, and followed on every request.
package plan

import (
	"encoding"
	"errors"
	"fmt"
	"net/textproto"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Source is where the value of a bound field comes from.
type Source int

// The sources of bound fields, one per struct tag.
const (
	Path Source = iota + 1
	Query
	Header
)

// Tag returns the struct tag that binds a field to s: "path", "query" or
// "header".
func (s Source) Tag() string {
	switch s {
	case Path:
		return "path"
	case Query:
		return "query"
	}
	return "header"
}

// String describes s for clients, e.g. "query parameter".
func (s Source) String() string {
	switch s {
	case Path:
		return "path parameter"
	case Query:
		return "query parameter"
	}
	return "header"
}

// Field is a field of a request bound from the path, the query or a header.
type Field struct {
	Source Source
	Name   string // the wildcard, the parameter or the header in the tag
	GoName string // the name of the field in Go
	JSON   string // the JSON name of the field

	index int
	set   func(v reflect.Value, values []string) (detail string)
}

// Binding binds the fields of a request type.
type Binding struct {
	Fields []Field // in the order of the struct
}

// Problem is a value that doesn't fit its field.
type Problem struct {
	Field  *Field
	Detail string // what's wrong with the value, e.g. "must be an integer"
}

// NewBinding returns the binding of the struct type t. A field may have one
// of the tags path, query or header, which name the wildcard, parameter or
// header it gets its value from. The field must be of type string, bool, an
// integer or a float type, or implement encoding.TextUnmarshaler, like
// time.Time does, or be a pointer to one of those, which gets a value only
// when there is one; query fields may also be slices of those types.
//
// NewBinding fails on a field of another type, on a tag that names nothing
// or repeats a name, on a field with more than one tag, and on a tag on an
// unexported field or on a field of an embedded struct.
func NewBinding(t reflect.Type) (*Binding, error) {
	b := &Binding{}
	seen := make(map[Source]map[string]string) // name → Go name
	for _, f := range reflect.VisibleFields(t) {
		var tagged []Source
		for _, src := range [...]Source{Path, Query, Header} {
			if _, ok := f.Tag.Lookup(src.Tag()); ok {
				tagged = append(tagged, src)
			}
		}
		if len(tagged) == 0 {
			continue
		}

		src := tagged[0]
		name := f.Tag.Get(src.Tag())
		tag := fmt.Sprintf("%s:%q", src.Tag(), name)
		switch {
		case len(tagged) > 1:
			return nil, fmt.Errorf("field %s has both %s and %s tags", f.Name, tagged[0].Tag(), tagged[1].Tag())
		case len(f.Index) > 1:
			return nil, fmt.Errorf("field %s has %s, but only fields of %v itself can be bound, not of embedded structs", f.Name, tag, t)
		case !f.IsExported():
			return nil, fmt.Errorf("field %s has %s, but it is unexported", f.Name, tag)
		case name == "":
			return nil, fmt.Errorf("field %s has an empty %s tag", f.Name, src.Tag())
		}
		if src == Header {
			name = textproto.CanonicalMIMEHeaderKey(name)
		}
		if seen[src] == nil {
			seen[src] = make(map[string]string)
		}
		if other, ok := seen[src][name]; ok {
			return nil, fmt.Errorf("fields %s and %s both have %s", other, f.Name, tag)
		}
		seen[src][name] = f.Name

		set := setter(f.Type, src == Query)
		if set == nil {
			msg := fmt.Sprintf("field %s has %s, but its type %v can't be bound", f.Name, tag, f.Type)
			if f.Type.Kind() == reflect.Slice && setter(f.Type, true) != nil {
				msg += ": slices can be bound only from the query"
			}
			return nil, errors.New(msg)
		}
		b.Fields = append(b.Fields, Field{
			Source: src,
			Name:   name,
			GoName: f.Name,
			JSON:   jsonName(f),
			index:  f.Index[0],
			set:    set,
		})
	}
	return b, nil
}

// Bind sets the bound fields of v, a struct of the binding's type, from the
// values that get returns for a source and a name. get reports false for a
// missing value, which leaves its field unchanged; a field that isn't a
// slice gets the first value. Bind returns a problem for every value that
// doesn't fit its field.
func (b *Binding) Bind(v reflect.Value, get func(src Source, name string) ([]string, bool)) []Problem {
	var problems []Problem
	for i := range b.Fields {
		f := &b.Fields[i]
		values, ok := get(f.Source, f.Name)
		if !ok {
			continue
		}
		if detail := f.set(v.Field(f.index), values); detail != "" {
			problems = append(problems, Problem{Field: f, Detail: detail})
		}
	}
	return problems
}

// Describe says what a value of type t must be, for clients and without
// Go type names: "must be an integer", "must be an RFC 3339 time". A nil t
// gets "has an invalid value".
func Describe(t reflect.Type) string {
	if t == nil {
		return "has an invalid value"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[time.Time]() {
		return "must be an RFC 3339 time"
	}
	switch t.Kind() {
	case reflect.String:
		return "must be a string"
	case reflect.Bool:
		return "must be true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "must be an integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "must be a non-negative integer"
	case reflect.Float32, reflect.Float64:
		return "must be a number"
	case reflect.Slice, reflect.Array:
		return "must be an array"
	case reflect.Map, reflect.Struct:
		return "must be an object"
	}
	return "has an invalid value"
}

// setter returns the function that sets a field of type t from its values,
// or nil if t can't be bound. Slices are allowed if slices is set.
func setter(t reflect.Type, slices bool) func(v reflect.Value, values []string) string {
	if parse := scalar(t); parse != nil {
		return func(v reflect.Value, values []string) string {
			return parse(v, values[0])
		}
	}
	if t.Kind() == reflect.Pointer {
		if parse := scalar(t.Elem()); parse != nil {
			return func(v reflect.Value, values []string) string {
				p := reflect.New(t.Elem())
				if detail := parse(p.Elem(), values[0]); detail != "" {
					return detail
				}
				v.Set(p)
				return ""
			}
		}
	}
	if !slices || t.Kind() != reflect.Slice {
		return nil
	}
	parse := scalar(t.Elem())
	if parse == nil {
		return nil
	}
	return func(v reflect.Value, values []string) string {
		s := reflect.MakeSlice(t, len(values), len(values))
		for i, value := range values {
			if detail := parse(s.Index(i), value); detail != "" {
				return detail
			}
		}
		v.Set(s)
		return ""
	}
}

// scalar returns the function that parses a string into a value of type t
// and returns what's wrong with it, if anything, or nil if t can't be
// bound.
func scalar(t reflect.Type) func(v reflect.Value, s string) string {
	if reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]()) {
		isTime := t == reflect.TypeFor[time.Time]()
		return func(v reflect.Value, s string) string {
			if err := v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s)); err != nil {
				if isTime {
					return Describe(t)
				}
				return err.Error() // the type's own words
			}
			return ""
		}
	}

	switch t.Kind() {
	case reflect.String:
		return func(v reflect.Value, s string) string {
			v.SetString(s)
			return ""
		}
	case reflect.Bool:
		return func(v reflect.Value, s string) string {
			x, err := strconv.ParseBool(s)
			if err != nil {
				return Describe(t)
			}
			v.SetBool(x)
			return ""
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bits := t.Bits()
		return func(v reflect.Value, s string) string {
			x, err := strconv.ParseInt(s, 10, bits)
			if errors.Is(err, strconv.ErrRange) {
				return fmt.Sprintf("must be an integer from %d to %d", int64(-1)<<(bits-1), int64(1)<<(bits-1)-1)
			} else if err != nil {
				return Describe(t)
			}
			v.SetInt(x)
			return ""
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		bits := t.Bits()
		return func(v reflect.Value, s string) string {
			x, err := strconv.ParseUint(s, 10, bits)
			if errors.Is(err, strconv.ErrRange) {
				return fmt.Sprintf("must be an integer from 0 to %d", uint64(1)<<bits-1)
			} else if err != nil {
				return Describe(t)
			}
			v.SetUint(x)
			return ""
		}
	case reflect.Float32, reflect.Float64:
		bits := t.Bits()
		return func(v reflect.Value, s string) string {
			x, err := strconv.ParseFloat(s, bits)
			if err != nil {
				return Describe(t)
			}
			v.SetFloat(x)
			return ""
		}
	}
	return nil
}

// jsonName returns the name of f in JSON: the name in its json tag, or its
// Go name.
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" || name == "-" {
		return f.Name
	}
	return name
}
