// Package apperror defines the typed business errors every use case returns,
// so handlers never have to guess an HTTP status or message from a bare
// error string, and infrastructure failures are never shown to clients.
package apperror

import "net/http"

// Code is a stable, machine-readable error identifier safe to return to
// clients.
type Code string

const (
	CodeValidation   Code = "validation_error"
	CodeNotFound     Code = "not_found"
	CodeConflict     Code = "conflict"
	CodeUnauthorized Code = "unauthorized"
	CodeForbidden    Code = "forbidden"
	CodeInternal     Code = "internal_error"
)

// Error is a business error with an HTTP status and a message safe to show
// to end users. Err optionally wraps the underlying infrastructure error for
// logging; it is never serialized to the client.
type Error struct {
	Code    Code
	Message string
	Status  int
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

func Validation(message string) *Error {
	return &Error{Code: CodeValidation, Message: message, Status: http.StatusBadRequest}
}

func NotFound(message string) *Error {
	return &Error{Code: CodeNotFound, Message: message, Status: http.StatusNotFound}
}

func Conflict(message string) *Error {
	return &Error{Code: CodeConflict, Message: message, Status: http.StatusConflict}
}

func Unauthorized(message string) *Error {
	return &Error{Code: CodeUnauthorized, Message: message, Status: http.StatusUnauthorized}
}

func Forbidden(message string) *Error {
	return &Error{Code: CodeForbidden, Message: message, Status: http.StatusForbidden}
}

// Internal wraps an infrastructure error (DB, network, ...). The wrapped
// error is only ever logged, never sent to the client.
func Internal(err error) *Error {
	return &Error{Code: CodeInternal, Message: "Something went wrong", Status: http.StatusInternalServerError, Err: err}
}
