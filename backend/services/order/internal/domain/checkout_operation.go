package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
)

// CheckoutOperation is one buyer checkout attempt identified by the
// client's Idempotency-Key. Its stored outcome is what a retry returns.
type CheckoutOperation struct {
	ID             string
	BuyerID        string
	IdempotencyKey string
	RequestHash    string
	Status         CheckoutOperationStatus
	OrderID        *string
	ErrorCode      *string
	ErrorMessage   *string
	ErrorStatus    *int
	ExpiresAt      time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CheckoutOperationStatus string

const (
	CheckoutOpPreparing CheckoutOperationStatus = "preparing"
	CheckoutOpCompleted CheckoutOperationStatus = "completed"
	CheckoutOpFailed    CheckoutOperationStatus = "failed"
)

const (
	// CheckoutKeyTTL is how long a key keeps returning its first outcome.
	CheckoutKeyTTL = 24 * time.Hour
	// CheckoutStaleAfter is when a preparing operation is considered
	// abandoned (process crash) and handed to recovery.
	CheckoutStaleAfter = 2 * time.Minute
)

// CheckoutRequest is what identifies "the same checkout" for idempotency.
type CheckoutRequest struct {
	AddressID     string
	CartVersion   *int64
	ExpectedTotal *int64
}

// Hash fingerprints the request; any difference in address, reviewed cart
// version or confirmed total is a different request.
func (r CheckoutRequest) Hash() string {
	parts := []string{"address=" + r.AddressID}
	if r.CartVersion != nil {
		parts = append(parts, "cart="+strconv.FormatInt(*r.CartVersion, 10))
	}
	if r.ExpectedTotal != nil {
		parts = append(parts, "total="+strconv.FormatInt(*r.ExpectedTotal, 10))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// ValidateIdempotencyKey accepts the printable keys clients generate (UUIDs).
func ValidateIdempotencyKey(key string) error {
	if len(key) < 8 || len(key) > 128 {
		return apperror.Validation("Idempotency-Key must be between 8 and 128 characters")
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return apperror.Validation("Idempotency-Key must be printable ASCII")
		}
	}
	return nil
}

// StoredError rebuilds the business error a failed operation returned, so a
// retry with the same key gets the same answer.
func (o *CheckoutOperation) StoredError() *apperror.Error {
	e := &apperror.Error{Code: apperror.CodeConflict, Status: 409, Message: "Checkout failed"}
	if o.ErrorCode != nil {
		e.Code = apperror.Code(*o.ErrorCode)
	}
	if o.ErrorMessage != nil {
		e.Message = *o.ErrorMessage
	}
	if o.ErrorStatus != nil {
		e.Status = *o.ErrorStatus
	}
	return e
}

// ReplayableFailure reports whether a failure should be returned again for
// the same key. Business refusals (4xx) are; infrastructure failures (5xx)
// are not, so a retry after an outage can succeed.
func ReplayableFailure(err *apperror.Error) bool {
	return err != nil && err.Status >= 400 && err.Status < 500
}
