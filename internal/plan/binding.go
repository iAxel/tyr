// Package plan builds the per-type plans that are made once, when an API is
// set up, and followed on every request.
package plan

import (
	"encoding"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
	GoName string // the name of the field in Go, with the embedded structs on the way
	JSON   string // the name of the field's member in the JSON object

	index []int // through embedded structs
	typ   reflect.Type
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
// when there is one; query fields may also be slices of those types. A
// number is written as in JSON: in decimal, without NaN, infinities, a plus
// sign, leading zeros or underscores. A time.Time is an RFC 3339 time in
// the path and the query, and in a header an HTTP date, as RFC 9110 has
// them, or else an RFC 3339 time, for headers of one's own.
//
// A bound field must be a member of t's JSON object, as encoding/json/v2
// sees it, so that a client can set it in JSON too: a field of t or of a
// struct embedded in it that no other field hides.
//
// NewBinding fails on a field of another type, on a tag that names nothing
// or repeats a name, on a query tag with a comma or a space, on a header
// tag that isn't a header name, on a field with more than one tag, and on a
// tag on a field that isn't a member of the JSON object.
func NewBinding(t reflect.Type) (*Binding, error) {
	b := &Binding{}
	var ms []member // of t's JSON object, found for the first field to need them
	var found bool
	seen := make(map[Source]map[string]string) // name → Go name
	for _, tf := range taggedFields(t) {
		f := tf.field
		var tags []Source
		for _, src := range [...]Source{Path, Query, Header} {
			if _, ok := f.Tag.Lookup(src.Tag()); ok {
				tags = append(tags, src)
			}
		}
		src := tags[0]
		name := f.Tag.Get(src.Tag())
		tag := fmt.Sprintf("%s:%q", src.Tag(), name)
		switch {
		case len(tags) > 1:
			return nil, fmt.Errorf("field %s has both %s and %s tags", tf.name, tags[0].Tag(), tags[1].Tag())
		case !f.IsExported():
			return nil, fmt.Errorf("field %s has %s, but it is unexported", tf.name, tag)
		case tf.nested:
			return nil, fmt.Errorf("field %s has %s, but only fields of %v and of structs embedded in it can be bound", tf.name, tag, t)
		case name == "":
			return nil, fmt.Errorf("field %s has an empty %s tag", tf.name, src.Tag())
		case src == Query && strings.ContainsFunc(name, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }):
			return nil, fmt.Errorf("field %s has %s, which isn't a query parameter name: it has a comma or a space", tf.name, tag)
		case src == Header && !isToken(name):
			return nil, fmt.Errorf("field %s has %s, which isn't a header name", tf.name, tag)
		}
		if !found {
			var err error
			if ms, err = members(t); err != nil {
				return nil, err
			}
			found = true
		}
		i := slices.IndexFunc(ms, func(m member) bool { return slices.Equal(m.index, tf.index) })
		if i < 0 {
			return nil, fmt.Errorf("field %s has %s, but the JSON object of %v has no member for it", tf.name, tag, t)
		}
		if src == Header {
			name = textproto.CanonicalMIMEHeaderKey(name)
		}
		if seen[src] == nil {
			seen[src] = make(map[string]string)
		}
		if other, ok := seen[src][name]; ok {
			return nil, fmt.Errorf("fields %s and %s both have %s", other, tf.name, tag)
		}
		seen[src][name] = tf.name

		set := setter(f.Type, src)
		if set == nil {
			msg := fmt.Sprintf("field %s has %s, but its type %v can't be bound", tf.name, tag, f.Type)
			if f.Type.Kind() == reflect.Slice && setter(f.Type, Query) != nil {
				msg += ": slices can be bound only from the query"
			}
			return nil, errors.New(msg)
		}
		b.Fields = append(b.Fields, Field{
			Source: src,
			Name:   name,
			GoName: tf.name,
			JSON:   ms[i].name,
			index:  tf.index,
			typ:    f.Type,
			set:    set,
		})
	}
	return b, nil
}

// taggedField is a field with a binding tag.
type taggedField struct {
	field  reflect.StructField
	name   string // the Go name, with the structs on the way
	index  []int
	nested bool // a field of a nested, not embedded, struct is on the way
}

// taggedFields returns the fields of the struct type t with a binding tag,
// at any depth: in t and in the structs embedded or nested in it.
func taggedFields(t reflect.Type) []taggedField {
	var out []taggedField
	var walk func(t reflect.Type, name string, index []int, nested bool, seen map[reflect.Type]bool)
	walk = func(t reflect.Type, name string, index []int, nested bool, seen map[reflect.Type]bool) {
		if seen[t] {
			return // a recursive type
		}
		seen[t] = true
		defer delete(seen, t)
		for i := range t.NumField() {
			sf := t.Field(i)
			path := sf.Name
			if name != "" {
				path = name + "." + sf.Name
			}
			index := append(slices.Clip(index), i)
			for _, src := range [...]Source{Path, Query, Header} {
				if _, ok := sf.Tag.Lookup(src.Tag()); ok {
					out = append(out, taggedField{field: sf, name: path, index: index, nested: nested})
					break
				}
			}
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !ft.ConvertibleTo(reflect.TypeFor[time.Time]()) {
				walk(ft, path, index, nested || !sf.Anonymous, seen)
			}
		}
	}
	walk(t, "", nil, false, make(map[reflect.Type]bool))
	return out
}

// Bind sets the bound fields of v, a struct of the binding's type, from the
// values that get returns for a source and a name. get reports false for a
// missing value, which leaves its field unchanged; a field that isn't a
// slice gets the first value. A value that fits is set, allocating nil
// embedded structs on the way to its field. Bind returns a problem for
// every field whose values don't fit it or aren't valid UTF-8, as JSON
// strings are.
func (b *Binding) Bind(v reflect.Value, get func(src Source, name string) ([]string, bool)) []Problem {
	var problems []Problem
	for i := range b.Fields {
		f := &b.Fields[i]
		values, ok := get(f.Source, f.Name)
		if !ok {
			continue
		}
		if slices.ContainsFunc(values, func(s string) bool { return !utf8.ValidString(s) }) {
			problems = append(problems, Problem{Field: f, Detail: "must be valid UTF-8"})
			continue
		}
		x := reflect.New(f.typ).Elem()
		if detail := f.set(x, values); detail != "" {
			problems = append(problems, Problem{Field: f, Detail: detail})
			continue
		}
		dst, _ := fieldByIndex(v, f.index, true)
		dst.Set(x)
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

// setter returns the function that sets a field of type t from its values
// in src, or nil if t can't be bound from src. Only the query has slices.
func setter(t reflect.Type, src Source) func(v reflect.Value, values []string) string {
	if parse := scalar(t, src); parse != nil {
		return func(v reflect.Value, values []string) string {
			return parse(v, values[0])
		}
	}
	if t.Kind() == reflect.Pointer {
		if parse := scalar(t.Elem(), src); parse != nil {
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
	if src != Query || t.Kind() != reflect.Slice {
		return nil
	}
	parse := scalar(t.Elem(), src)
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

// scalar returns the function that parses a string from src into a value of
// type t and returns what's wrong with it, if anything, or nil if t can't be
// bound.
func scalar(t reflect.Type, src Source) func(v reflect.Value, s string) string {
	if t == reflect.TypeFor[time.Time]() && src == Header {
		return func(v reflect.Value, s string) string {
			tm, err := http.ParseTime(s)
			if err == nil {
				tm = tm.UTC() // GMT, as HTTP dates are
			} else if err := tm.UnmarshalText([]byte(s)); err != nil {
				return "must be an HTTP date or an RFC 3339 time"
			}
			v.Set(reflect.ValueOf(tm))
			return ""
		}
	}
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
			if !isNumber(s) {
				return Describe(t)
			}
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
			if !isNumber(s) {
				return Describe(t)
			}
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
			if !isNumber(s) {
				return Describe(t)
			}
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

// isNumber reports whether s is a number as JSON writes one, so that a
// value means the same in the path, the query or a header as in a body:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?. strconv parses more, such
// as NaN, Inf, 0x10, 1_000 and +1.
func isNumber(s string) bool {
	digits := func(i int) int { // the end of the digits at s[i:]
		for i < len(s) && '0' <= s[i] && s[i] <= '9' {
			i++
		}
		return i
	}
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && '1' <= s[i] && s[i] <= '9':
		i = digits(i)
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		j := digits(i + 1)
		if j == i+1 {
			return false // no digits after the point
		}
		i = j
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		j := digits(i)
		if j == i {
			return false // no digits in the exponent
		}
		i = j
	}
	return i == len(s)
}
