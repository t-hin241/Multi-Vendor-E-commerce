// Package httpresponse defines the response envelope shared by every public
// endpoint so buyer, vendor and admin clients can rely on one error shape.
package httpresponse

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
)

// ErrorBody is the unified error payload. Code is a stable machine-readable
// identifier; Message is safe to show to end users. RequestID lets support
// correlate a client-reported error with server logs without exposing
// internals like stack traces.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

type successEnvelope struct {
	Data any `json:"data"`
}

// OK writes a successful response wrapped in the standard {"data": ...} envelope.
func OK(c *gin.Context, status int, data any) {
	c.JSON(status, successEnvelope{Data: data})
}

// Error writes the standard error envelope. It never includes a stack trace
// or internal error detail, only a stable code, a user-safe message and the
// request id for log correlation.
func Error(c *gin.Context, status int, code, message string) {
	requestID, _ := c.Get("request_id")
	requestIDStr, _ := requestID.(string)

	c.JSON(status, errorEnvelope{Error: ErrorBody{
		Code:      code,
		Message:   message,
		RequestID: requestIDStr,
	}})
}

// HandleError translates a use-case error into the standard error envelope.
// A *apperror.Error is rendered using its own code/status/message; anything
// else is treated as an unexpected infrastructure failure, logged with full
// detail server-side (never sent to the client) and reported as a generic
// 500.
func HandleError(c *gin.Context, log zerolog.Logger, err error) {
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		if appErr.Code == apperror.CodeInternal {
			log.Error().
				Str("request_id", requestIDFrom(c)).
				Err(appErr.Unwrap()).
				Msg("internal_error")
		}
		Error(c, appErr.Status, string(appErr.Code), appErr.Message)
		return
	}

	log.Error().
		Str("request_id", requestIDFrom(c)).
		Err(err).
		Msg("unhandled_error")
	Error(c, 500, string(apperror.CodeInternal), "Something went wrong")
}

func requestIDFrom(c *gin.Context) string {
	v, _ := c.Get("request_id")
	s, _ := v.(string)
	return s
}
