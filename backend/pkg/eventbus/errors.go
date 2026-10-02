package eventbus

import (
	"errors"

	"shopee/backend/pkg/apperror"
)

type permanentError struct{ err error }

func (p permanentError) Error() string { return p.err.Error() }
func (p permanentError) Unwrap() error { return p.err }

// Permanent marks a failure that redelivery cannot fix (malformed event,
// contradicting state): the event is parked at once.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent: marked Permanent, or a business refusal (4xx other than
// 401/403/408/429). Infrastructure errors are transient.
func IsPermanent(err error) bool {
	var p permanentError
	if errors.As(err, &p) {
		return true
	}
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code == apperror.CodeInternal {
		return false
	}
	switch app.Status {
	case 401, 403, 408, 429:
		return false
	}
	return app.Status >= 400 && app.Status < 500
}
