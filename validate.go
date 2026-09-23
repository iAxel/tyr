package tyr

import "errors"

// Validator is implemented by requests that check themselves, for rules
// that struct tags can't express: cross-field checks, regular expressions,
// lookups in memory. Validate gets no context, so it must not do I/O:
// checks that need it belong in the handler.
//
// [Operation.Call] calls Validate after the interceptors, right before the
// handler, if *Req implements Validator; Validate may have a value or a
// pointer receiver, and it isn't called for nested structs. An error that
// contains an [Error], such as one from [Violations.Err], is used as is.
// Any other error becomes [KindInvalidArgument] with the error's text as
// its message, since Validate writes its errors for the client, and the
// error stays its cause. The mappers of [API.MapError] don't see errors of
// Validate.
type Validator interface {
	Validate() error
}

// validationError turns an error of Validate into an Error, as described
// at Validator.
func (a *API) validationError(err error) *Error {
	if _, ok := errors.AsType[*Error](err); ok {
		return a.resolve(err) // as is; a nil *Error becomes internal
	}
	return &Error{Kind: KindInvalidArgument, Message: err.Error(), cause: err}
}
