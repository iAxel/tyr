// Package plantest holds the request types and values that tests check the
// plans of package plan against: the field layouts against what
// encoding/json/v2 writes and reads, and the validate tags against
// go-playground/validator, in the tests of the core here and of the
// validate/playground module.
package plantest

import "time"

// Layouts returns zero values of struct types whose fields json/v2 lays out
// in each way the plans must follow: flat, embedded, embedded through a
// pointer or with a JSON name, nested, hidden by a shallower field, tied at
// one depth, promoted with the embed option, and embedded from an
// unexported type.
func Layouts() []any {
	return []any{
		Flat{hidden: ""},
		Embedded{},
		EmbeddedPointer{},
		EmbeddedNamed{},
		Nested{},
		Shadowed{},
		TaggedWins{},
		Conflicting{},
		EmbedOption{},
		UnexportedEmbedded{},
		Deep{},
		Fallback{},
	}
}

// Fallback keeps unknown members in a field with the embed option, which
// isn't a member itself.
type Fallback struct {
	Own  string         `json:"own"`
	Rest map[string]any `json:",embed"` //nolint:staticcheck // SA5008 doesn't know the embed option of json/v2
}

// Base is embedded in layouts.
type Base struct {
	ID   string `json:"id"`
	Name string // named after the field
}

// Inner is nested in layouts.
type Inner struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// Flat has no embedded or nested structs.
type Flat struct {
	A       string `json:"a"`
	B       int    `json:"b"`
	C       string // named after the field
	Ignored string `json:"-"`
	hidden  string
}

// Embedded embeds a struct, whose fields JSON promotes.
type Embedded struct {
	Base
	Own string `json:"own"`
}

// EmbeddedPointer embeds a struct through a pointer.
type EmbeddedPointer struct {
	*Base
	Own string `json:"own"`
}

// EmbeddedNamed embeds a struct with a JSON name, which makes it a member.
type EmbeddedNamed struct {
	Base `json:"base"`
	Own  string `json:"own"`
}

// Nested has struct fields, which are members of their own.
type Nested struct {
	Inner Inner  `json:"inner"`
	Ptr   *Inner `json:"ptr"`
}

// Shadowed has a field that hides a field of its embedded struct.
type Shadowed struct {
	Base
	ID string `json:"id"`
}

// TaggedA has a field with a JSON name in its tag.
type TaggedA struct {
	Name string `json:"Name"`
}

// TaggedB has a field of the same JSON name without a tag.
type TaggedB struct {
	Name string
}

// TaggedWins embeds two fields of one name at one depth: the tagged wins.
type TaggedWins struct {
	TaggedA
	TaggedB
}

// ConflictA has a field without a tag.
type ConflictA struct {
	Value string
}

// ConflictB has a field of the same name without a tag.
type ConflictB struct {
	Value string
}

// Conflicting embeds two untagged fields of one name at one depth: neither
// is a member.
type Conflicting struct {
	ConflictA
	ConflictB
	Own string `json:"own"`
}

// EmbedOption promotes the members of a field with the embed option.
type EmbedOption struct {
	Opts Inner  `json:",embed"` //nolint:staticcheck // SA5008 doesn't know the embed option of json/v2
	Own  string `json:"own"`
}

// unexportedBase is an unexported type with exported fields.
type unexportedBase struct {
	Key string `json:"key"`
}

// UnexportedEmbedded embeds a struct of an unexported type.
type UnexportedEmbedded struct {
	unexportedBase
	Own string `json:"own"`
}

// Middle embeds Base and is embedded in Deep.
type Middle struct {
	Base
	Mid string `json:"mid"`
}

// Deep embeds structs two levels down.
type Deep struct {
	Middle
	Own string `json:"own"`
}

// Violation is a violation of a validate tag as the core reports it.
type Violation struct {
	Pointer string
	Detail  string
}

// Check is a value and the violations of its validate tags.
type Check struct {
	Name  string
	Value any
	Want  []Violation
}

// Strings has rules for strings.
type Strings struct {
	Required string `json:"required" validate:"required"`
	Min      string `json:"min" validate:"min=2"`
	Max      string `json:"max" validate:"max=3"`
	Len      string `json:"len" validate:"len=2"`
	Runes    string `json:"runes" validate:"max=2"`
	OneOf    string `json:"one_of" validate:"oneof=red green 'light blue'"`
	Email    string `json:"email" validate:"omitempty,email"`
	URL      string `json:"url" validate:"omitempty,url"`
	UUID     string `json:"uuid" validate:"omitempty,uuid"`
}

// Numbers has rules for numbers.
type Numbers struct {
	Required int           `json:"required" validate:"required"`
	Gt       int           `json:"gt" validate:"gt=0"`
	Gte      int8          `json:"gte" validate:"gte=-1"`
	Lt       uint          `json:"lt" validate:"lt=10"`
	Lte      float64       `json:"lte" validate:"lte=1.5"`
	Hex      int           `json:"hex" validate:"max=0x10"`
	OneOf    int           `json:"one_of" validate:"oneof=1 2 3"`
	Timeout  time.Duration `json:"timeout" validate:"min=1s"`
}

// Collections has rules for slices, maps and arrays.
type Collections struct {
	Tags  []string          `json:"tags" validate:"min=1,max=2"`
	Attrs map[string]string `json:"attrs" validate:"required"`
	Pair  [2]int            `json:"pair" validate:"len=2"`
	Empty []string          `json:"empty" validate:"omitempty,min=2"`
}

// Pointers has rules for pointers.
type Pointers struct {
	Required *int    `json:"required" validate:"required"`
	Omit     *int    `json:"omit" validate:"omitempty,min=5"`
	First    *string `json:"first" validate:"min=2"`
	Zero     *int    `json:"zero" validate:"required"`
}

// Profile is nested in Nesting.
type Profile struct {
	Color string `json:"color" validate:"required,oneof=red green"`
}

// Paging is embedded in Nesting.
type Paging struct {
	Limit int `json:"limit" validate:"max=100"`
}

// Nesting has nested and embedded structs.
type Nesting struct {
	Profile  Profile   `json:"profile"`
	Ptr      *Profile  `json:"ptr"`
	Required Profile   `json:"required" validate:"required"`
	Omitted  *Profile  `json:"omitted" validate:"omitempty"`
	Items    []Profile `json:"items"`
	Paging
}

// Times has rules for times.
type Times struct {
	At  time.Time  `json:"at" validate:"required"`
	Opt *time.Time `json:"opt" validate:"omitempty"`
}

// Order has fields out of alphabetical order.
type Order struct {
	B string `json:"b" validate:"required"`
	A string `json:"a" validate:"required"`
}

// Skipped has fields that JSON or validation leaves out.
type Skipped struct {
	Ignored string `json:"-" validate:"required"`
	hidden  string `validate:"required"`
	Dash    string `json:"dash" validate:"-"`
}

// Formats has more values for the rules of formats and oneof.
type Formats struct {
	Email    string `json:"email" validate:"email"`
	File     string `json:"file" validate:"url"`
	Opaque   string `json:"opaque" validate:"url"`
	Fragment string `json:"fragment" validate:"url"`
	Upper    string `json:"upper" validate:"url"`
	UUID     string `json:"uuid" validate:"uuid"`
	UUIDHex  string `json:"uuid_hex" validate:"uuid"`
	Level    uint8  `json:"level" validate:"oneof=1 2"`
}

// Checks returns values with valid and invalid fields and the violations
// the core reports for them: for each field, the first rule it fails, in
// the order of the fields.
func Checks() []Check {
	return []Check{
		{
			Name: "valid strings",
			Value: Strings{
				Required: "x", Min: "ab", Max: "abc", Len: "ab", Runes: "ёж",
				OneOf: "light blue", Email: "ann@go.dev", URL: "https://go.dev",
				UUID: "F81D4FAE-7DEC-11D0-A765-00A0C91E6BF6",
			},
		},
		{
			Name: "invalid strings",
			Value: Strings{
				Min: "a", Max: "abcd", Len: "a", Runes: "ёжик", OneOf: "blue",
				Email: "Ann <ann@go.dev>", URL: "go.dev", UUID: "f81d4fae7dec11d0a76500a0c91e6bf6",
			},
			Want: []Violation{
				{"/required", "is required"},
				{"/min", "must be at least 2 characters"},
				{"/max", "must be at most 3 characters"},
				{"/len", "must be exactly 2 characters"},
				{"/runes", "must be at most 2 characters"},
				{"/one_of", "must be one of: red, green, light blue"},
				{"/email", "must be an email address"},
				{"/url", "must be a URL"},
				{"/uuid", "must be a UUID"},
			},
		},
		{
			Name: "valid formats",
			Value: Formats{
				Email: "ann@go.dev", File: "file:///tmp/x", Opaque: "mailto:ann@go.dev",
				Fragment: "x:/#top", Upper: "HTTPS://GO.DEV",
				UUID: "f81d4fae-7dec-11d0-a765-00a0c91e6bf6", UUIDHex: "F81D4FAE-7DEC-11D0-A765-00A0C91E6BF6",
				Level: 2,
			},
		},
		{
			Name: "invalid formats",
			Value: Formats{
				Email: "no-at-sign", File: "file:///", Opaque: "mailto:", Fragment: "x:/",
				UUID: "f81d4fae_7dec-11d0-a765-00a0c91e6bf6", UUIDHex: "f81d4fae-7dec-11d0-a765-00a0c91e6bfg",
				Level: 3,
			},
			Want: []Violation{
				{"/email", "must be an email address"},
				{"/file", "must be a URL"},
				{"/opaque", "must be a URL"},
				{"/fragment", "must be a URL"},
				{"/upper", "must be a URL"},
				{"/uuid", "must be a UUID"},
				{"/uuid_hex", "must be a UUID"},
				{"/level", "must be one of: 1, 2"},
			},
		},
		{
			Name:  "valid numbers",
			Value: Numbers{Required: 1, Gt: 1, Gte: -1, Lt: 9, Lte: 1.5, Hex: 16, OneOf: 3, Timeout: time.Second},
		},
		{
			Name:  "invalid numbers",
			Value: Numbers{Gte: -2, Lt: 10, Lte: 2, Hex: 17, OneOf: 4, Timeout: time.Second / 2},
			Want: []Violation{
				{"/required", "is required"},
				{"/gt", "must be greater than 0"},
				{"/gte", "must be at least -1"},
				{"/lt", "must be less than 10"},
				{"/lte", "must be at most 1.5"},
				{"/hex", "must be at most 16"},
				{"/one_of", "must be one of: 1, 2, 3"},
				{"/timeout", "must be at least 1s"},
			},
		},
		{
			Name:  "valid collections",
			Value: Collections{Tags: []string{"a"}, Attrs: map[string]string{}},
		},
		{
			// An empty slice that isn't nil has a value: omitempty doesn't
			// skip it.
			Name:  "invalid collections",
			Value: Collections{Empty: []string{}},
			Want: []Violation{
				{"/tags", "must have at least 1 item"},
				{"/attrs", "is required"},
				{"/empty", "must have at least 2 items"},
			},
		},
		{
			// A nil pointer answers to its first rule; a pointer to zero
			// has a value.
			Name:  "pointers",
			Value: Pointers{Omit: new(3), Zero: new(0)},
			Want: []Violation{
				{"/required", "is required"},
				{"/omit", "must be at least 5"},
				{"/first", "must be at least 2 characters"},
			},
		},
		{
			// A zero struct fails required and isn't checked further;
			// slices of structs aren't checked without dive.
			Name: "nesting",
			Value: Nesting{
				Profile: Profile{Color: "blue"},
				Items:   []Profile{{}},
				Paging:  Paging{Limit: 101},
			},
			Want: []Violation{
				{"/profile/color", "must be one of: red, green"},
				{"/required", "is required"},
				{"/limit", "must be at most 100"},
			},
		},
		{
			Name:  "nested pointer",
			Value: Nesting{Ptr: &Profile{}, Required: Profile{Color: "red"}, Omitted: &Profile{Color: "red"}},
			Want: []Violation{
				{"/profile/color", "is required"},
				{"/ptr/color", "is required"},
			},
		},
		{
			Name:  "times",
			Value: Times{},
			Want:  []Violation{{"/at", "is required"}},
		},
		{
			Name:  "order of the fields",
			Value: Order{},
			Want:  []Violation{{"/b", "is required"}, {"/a", "is required"}},
		},
		{
			// A field JSON leaves out is still checked, under its Go name.
			Name:  "skipped fields",
			Value: Skipped{hidden: ""},
			Want:  []Violation{{"/Ignored", "is required"}},
		},
	}
}
