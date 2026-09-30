package domain

import (
	"errors"
	"math"
)

var errMissingCommissionRule = errors.New("no commission rule is configured")

// MulAmount multiplies two non-negative integers (minor units, quantities,
// basis points), reporting overflow instead of wrapping.
func MulAmount(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if a != 0 && b > math.MaxInt64/a {
		return 0, false
	}
	return a * b, true
}

// AddAmount adds two non-negative minor-unit amounts, reporting overflow.
func AddAmount(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}
