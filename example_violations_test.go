package tyr_test

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/iaxel/tyr"
)

type CreateLinkReq struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

var codeRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// Validate holds rules that struct tags can't express: cross-field checks,
// regular expressions, lookups.
func (r CreateLinkReq) Validate() error {
	var v tyr.Violations
	if r.Code != "" && !codeRe.MatchString(r.Code) {
		v.Add("code", "only a-z, 0-9 and '-'")
	}
	return v.Err()
}

func ExampleViolations() {
	fmt.Println(CreateLinkReq{URL: "https://go.dev", Code: "go"}.Validate())

	err := CreateLinkReq{URL: "https://go.dev", Code: "Go!"}.Validate()
	fmt.Println(err)
	if e, ok := errors.AsType[*tyr.Error](err); ok {
		for _, v := range e.Details.(tyr.Violations) {
			fmt.Println(v.Pointer, v.Detail)
		}
	}
	// Output:
	// <nil>
	// invalid_argument: validation failed
	// /code only a-z, 0-9 and '-'
}
