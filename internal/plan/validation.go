package plan

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Violation is a field that failed validation.
type Violation struct {
	Pointer string // JSON Pointer of the field
	Detail  string // what's wrong with it, for clients
}

// Validation checks values of a struct type against the validate tags of
// its fields, with the semantics of the same tags in go-playground/validator
// created WithRequiredStructEnabled: a field fails on its first failing
// rule, with one violation, and all fields are checked, in their order,
// through nested and embedded structs.
type Validation struct {
	root *validatedStruct
}

// validatedStruct is the plan of one struct type.
type validatedStruct struct {
	fields []validatedField
}

// validatedField is the plan of one field of a struct.
type validatedField struct {
	index   int
	segment string // of the JSON Pointer; "" for a struct embedded in JSON
	rules   []rule
	nested  *validatedStruct // for a field of a struct type or a pointer to one
}

// rule is one rule of a validate tag.
type rule struct {
	name   string
	detail string                                       // of a violation
	check  func(v reflect.Value, fromPointer bool) bool // v has no pointers left
}

// hint ends the message about a rule the core doesn't know.
const hint = "add the validate/playground module for more rules, or move the check to Validate()"

// NewValidation returns the validation of the struct type t, or nil if t
// has nothing to validate. It fails on an unknown rule, a rule that
// doesn't apply to its field's type, and a bad parameter.
func NewValidation(t reflect.Type) (*Validation, error) {
	b := &validationBuilder{plans: make(map[reflect.Type]*validatedStruct)}
	root, err := b.build(t, "")
	if err != nil || root == nil {
		return nil, err
	}
	return &Validation{root: root}, nil
}

// Validate returns the violations of v, a value of the validation's type,
// in the order of the fields.
func (p *Validation) Validate(v reflect.Value) []Violation {
	var out []Violation
	p.root.validate(v, "", &out)
	return out
}

func (s *validatedStruct) validate(v reflect.Value, prefix string, out *[]Violation) {
	for i := range s.fields {
		f := &s.fields[i]
		f.validate(v.Field(f.index), prefix+f.segment, out)
	}
}

func (f *validatedField) validate(v reflect.Value, pointer string, out *[]Violation) {
	// Like go-playground, go through pointers to the value. A nil one only
	// answers to the first rule: omitempty skips it, any other rule fails.
	fromPointer := false
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			if len(f.rules) > 0 && f.rules[0].name != "omitempty" {
				*out = append(*out, Violation{Pointer: pointer, Detail: f.rules[0].detail})
			}
			return
		}
		v = v.Elem()
		fromPointer = true
	}
	for _, r := range f.rules {
		if r.name == "omitempty" {
			if !hasValue(v, fromPointer) {
				return
			}
			continue
		}
		if !r.check(v, fromPointer) {
			*out = append(*out, Violation{Pointer: pointer, Detail: r.detail})
			return
		}
	}
	if f.nested != nil {
		f.nested.validate(v, pointer, out)
	}
}

// hasValue is hasValue of go-playground, which required and omitempty use:
// nil and zero values have none, while a value behind a non-nil pointer
// has one even if it is zero.
func hasValue(v reflect.Value, fromPointer bool) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map, reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func:
		return !v.IsNil()
	}
	return fromPointer || v.IsValid() && !v.IsZero()
}

// validationBuilder builds the plans of struct types, once per type, so
// that recursive types end.
type validationBuilder struct {
	plans map[reflect.Type]*validatedStruct
}

// build returns the plan of the struct type t, or nil if there is nothing
// to validate. path names t's field in messages.
func (b *validationBuilder) build(t reflect.Type, path string) (*validatedStruct, error) {
	if s, ok := b.plans[t]; ok {
		return s, nil
	}
	s := &validatedStruct{}
	b.plans[t] = s // a recursive type finds its plan while it's built
	for i := range t.NumField() {
		sf := t.Field(i)
		tag := sf.Tag.Get("validate")
		if !sf.IsExported() && !sf.Anonymous || tag == "-" {
			continue // as go-playground skips them
		}
		name := sf.Name
		if path != "" {
			name = path + "." + sf.Name
		}
		f := validatedField{index: i, segment: segment(sf)}

		value := sf.Type
		for value.Kind() == reflect.Pointer {
			value = value.Elem()
		}
		var err error
		if tag != "" {
			if f.rules, err = parseRules(name, tag, value); err != nil {
				return nil, err
			}
		}
		if value.Kind() == reflect.Struct && !value.ConvertibleTo(reflect.TypeFor[time.Time]()) {
			if f.nested, err = b.build(value, name); err != nil {
				return nil, err
			}
		}
		if len(f.rules) > 0 || f.nested != nil {
			s.fields = append(s.fields, f)
		}
	}
	if len(s.fields) == 0 {
		b.plans[t] = nil
		return nil, nil
	}
	return s, nil
}

// segment returns the segment of the JSON Pointer of sf's field: none for
// a struct that JSON embeds, the JSON name otherwise, and the Go name for
// a field that JSON leaves out.
func segment(sf reflect.StructField) string {
	name, tagged, embed, ignored := parseJSONTag(sf)
	if ignored {
		return pointer("", sf.Name)
	}
	t := sf.Type
	if t.Kind() == reflect.Pointer && t.Name() == "" {
		t = t.Elem()
	}
	if (embed || sf.Anonymous && !tagged) && t.Kind() == reflect.Struct {
		return ""
	}
	return pointer("", name)
}

// parseRules returns the rules of the validate tag of the field name, whose
// value, past any pointers, is of type t.
func parseRules(name, tag string, t reflect.Type) ([]rule, error) {
	if strings.Contains(tag, "|") {
		return nil, fmt.Errorf("field %s: validate:%q: alternatives with | aren't supported; %s", name, tag, hint)
	}
	var rules []rule
	for part := range strings.SplitSeq(tag, ",") {
		key, param, _ := strings.Cut(part, "=")
		r, err := newRule(key, param, t)
		if err != nil {
			return nil, fmt.Errorf("field %s: validate:%q: %w", name, tag, err)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// newRule returns the rule key=param for values of type t.
func newRule(key, param string, t reflect.Type) (rule, error) {
	r := rule{name: key}
	if (key == "required" || key == "omitempty") && param != "" {
		return r, fmt.Errorf("rule %q takes no parameter", key)
	}
	switch key {
	case "required":
		r.detail = "is required"
		r.check = hasValue
		return r, nil
	case "omitempty":
		return r, nil // handled by validatedField.validate
	}

	c, compares := comparisonOf(key)
	known := compares || key == "oneof" || key == "email" || key == "url" || key == "http_url" || key == "uuid"
	switch {
	case !known:
		return r, fmt.Errorf("unknown rule %q; %s", key, hint)
	case t.ConvertibleTo(reflect.TypeFor[time.Time]()):
		return r, fmt.Errorf("rule %q doesn't apply to %v: the core supports only required and omitempty for times", key, t)
	case compares:
		return compareRule(r, c, param, t)
	case key == "oneof":
		return oneOfRule(r, param, t)
	}
	return stringRule(r, param, t)
}

// comparison says how a rule compares a length or a number with its
// parameter, and how its violations read.
type comparison struct {
	op           operator
	chars, items string // for strings and for collections, with %s the count
	number       string // for numbers, with %s the parameter
}

// operator is how a rule compares a value with its parameter.
type operator int

const (
	atLeast operator = iota // >=
	atMost                  // <=
	exactly                 // ==
	above                   // >
	below                   // <
)

// comparisonOf returns the comparison of the rule key and reports whether
// key compares lengths or numbers.
func comparisonOf(key string) (comparison, bool) {
	switch key {
	case "min", "gte":
		return comparison{atLeast, "must be at least %s", "must have at least %s", "must be at least %s"}, true
	case "max", "lte":
		return comparison{atMost, "must be at most %s", "must have at most %s", "must be at most %s"}, true
	case "len":
		return comparison{exactly, "must be exactly %s", "must have exactly %s", "must be %s"}, true
	case "gt":
		return comparison{above, "must be more than %s", "must have more than %s", "must be greater than %s"}, true
	case "lt":
		return comparison{below, "must be fewer than %s", "must have fewer than %s", "must be less than %s"}, true
	}
	return comparison{}, false
}

// holds reports whether x compares with n as op says, with the operators of
// Go, as go-playground compares: NaN fails every comparison.
func holds[T int64 | uint64 | float64](op operator, x, n T) bool {
	switch op {
	case atLeast:
		return x >= n
	case atMost:
		return x <= n
	case exactly:
		return x == n
	case above:
		return x > n
	}
	return x < n
}

// compareRule returns a rule of cmp, which compares the length of a string
// in runes or of a collection, or the value of a number, with param.
func compareRule(r rule, c comparison, param string, t reflect.Type) (rule, error) {
	bad := func(err error) (rule, error) {
		return r, fmt.Errorf("rule %s=%s: bad parameter for %v: %w", r.name, param, t, err)
	}
	switch k := t.Kind(); {
	case k == reflect.String || k == reflect.Slice || k == reflect.Map || k == reflect.Array:
		n, err := strconv.ParseInt(param, 0, 64)
		if err != nil {
			return bad(err)
		}
		if k == reflect.String {
			r.detail = fmt.Sprintf(c.chars, count(n, "character"))
			r.check = func(v reflect.Value, _ bool) bool {
				return holds(c.op, int64(utf8.RuneCountInString(v.String())), n)
			}
		} else {
			r.detail = fmt.Sprintf(c.items, count(n, "item"))
			r.check = func(v reflect.Value, _ bool) bool { return holds(c.op, int64(v.Len()), n) }
		}
	case isInt(k):
		n, err := parseInt(param, t)
		if err != nil {
			return bad(err)
		}
		text := strconv.FormatInt(n, 10)
		if t == reflect.TypeFor[time.Duration]() {
			text = time.Duration(n).String()
		}
		r.detail = fmt.Sprintf(c.number, text)
		r.check = func(v reflect.Value, _ bool) bool { return holds(c.op, v.Int(), n) }
	case isUint(k):
		n, err := strconv.ParseUint(param, 0, 64)
		if err != nil {
			return bad(err)
		}
		r.detail = fmt.Sprintf(c.number, strconv.FormatUint(n, 10))
		r.check = func(v reflect.Value, _ bool) bool { return holds(c.op, v.Uint(), n) }
	case k == reflect.Float32 || k == reflect.Float64:
		n, err := strconv.ParseFloat(param, t.Bits())
		if err != nil {
			return bad(err)
		}
		r.detail = fmt.Sprintf(c.number, strconv.FormatFloat(n, 'g', -1, t.Bits()))
		r.check = func(v reflect.Value, _ bool) bool { return holds(c.op, v.Float(), n) }
	default:
		return r, fmt.Errorf("rule %q doesn't apply to %v", r.name, t)
	}
	return r, nil
}

// splitParams splits the parameter of oneof as go-playground does: into
// words or 'quoted phrases'.
var splitParams = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`'[^']*'|\S+`)
})

// oneOfRule returns a rule that checks that a string or an integer is one
// of the values in param, compared as text, as go-playground does.
func oneOfRule(r rule, param string, t reflect.Type) (rule, error) {
	values := splitParams().FindAllString(param, -1)
	for i, v := range values {
		values[i] = strings.ReplaceAll(v, "'", "")
	}
	if len(values) == 0 {
		return r, errors.New("rule oneof needs values")
	}
	var text func(v reflect.Value) string
	switch k := t.Kind(); {
	case k == reflect.String:
		text = reflect.Value.String
	case isInt(k):
		text = func(v reflect.Value) string { return strconv.FormatInt(v.Int(), 10) }
	case isUint(k):
		text = func(v reflect.Value) string { return strconv.FormatUint(v.Uint(), 10) }
	default:
		return r, fmt.Errorf("rule %q doesn't apply to %v", r.name, t)
	}
	r.detail = "must be one of: " + strings.Join(values, ", ")
	r.check = func(v reflect.Value, _ bool) bool {
		return slices.Contains(values, text(v))
	}
	return r, nil
}

// stringRule returns the rule email, url, http_url or uuid, which check
// strings; uuid also checks a fmt.Stringer, as go-playground does.
func stringRule(r rule, param string, t reflect.Type) (rule, error) {
	if param != "" {
		return r, fmt.Errorf("rule %q takes no parameter", r.name)
	}
	isString := t.Kind() == reflect.String
	stringer := r.name == "uuid" && t.Implements(reflect.TypeFor[fmt.Stringer]())
	if !isString && !stringer {
		return r, fmt.Errorf("rule %q doesn't apply to %v", r.name, t)
	}
	var matches func(s string) bool
	switch r.name {
	case "email":
		r.detail, matches = "must be an email address", isEmail
	case "url":
		r.detail, matches = "must be a URL", isURL
	case "http_url":
		r.detail, matches = "must be an http or https URL", isHTTPURL
	default:
		r.detail, matches = "must be a UUID", isUUID
	}
	r.check = func(v reflect.Value, _ bool) bool {
		if isString {
			return matches(v.String())
		}
		return matches(v.Interface().(fmt.Stringer).String())
	}
	return r, nil
}

// isURL is isURL of go-playground: a URL with a scheme and, but for file
// URLs, which need a path, a host, a fragment or an opaque part.
func isURL(s string) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return false
	}
	if u.Scheme == "file" {
		return u.Path != "" && u.Path != "/"
	}
	return u.Host != "" || u.Fragment != "" || u.Opaque != ""
}

// isHTTPURL is isHttpURL of go-playground: a URL, see isURL, with a host
// and the scheme http or https.
func isHTTPURL(s string) bool {
	if !isURL(s) {
		return false
	}
	u, err := url.Parse(strings.ToLower(s))
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// isUUID matches uUIDRegexString of go-playground: 8-4-4-4-12 hexadecimal
// digits of either case.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		switch c := s[i]; {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f', 'A' <= c && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// parseInt parses the parameter of a rule for a signed integer type t, as
// go-playground does: in any base with a prefix, or as a duration for
// time.Duration.
func parseInt(param string, t reflect.Type) (int64, error) {
	if t == reflect.TypeFor[time.Duration]() {
		if d, err := time.ParseDuration(param); err == nil {
			return int64(d), nil
		}
	}
	return strconv.ParseInt(param, 0, 64)
}

// count returns n of unit, in the plural unless n is 1.
func count(n int64, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.FormatInt(n, 10) + " " + unit + "s"
}

func isInt(k reflect.Kind) bool {
	return k >= reflect.Int && k <= reflect.Int64
}

func isUint(k reflect.Kind) bool {
	return k >= reflect.Uint && k <= reflect.Uint64
}
