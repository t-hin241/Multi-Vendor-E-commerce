package usecase

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/shipment/internal/carrier"
)

// PW-042: Vendor asks Shipment, which owns the carrier integration, to
// have the carrier check a shop's return destination.

// AddressCheck is the carrier's answer to an address check.
type AddressCheck struct {
	Deliverable bool
	Reason      string
	Reference   string
}

// CodeAddressCheckUnsupported: the carrier has no address check; an
// operator verifies the address.
const CodeAddressCheckUnsupported apperror.Code = "address_check_unsupported"

// CheckAddress asks the configured carrier. Unsupported and an
// unreachable carrier are distinct errors so the caller knows whether to
// retry.
func (uc *ShipmentUseCase) CheckAddress(ctx context.Context, in carrier.AddressCheckInput) (*AddressCheck, error) {
	for _, field := range []string{in.Province, in.District, in.StreetAddress} {
		if strings.TrimSpace(field) == "" {
			return nil, apperror.Validation("province, district and street_address are required")
		}
	}
	if uc.AddressChecker == nil {
		return nil, &apperror.Error{Code: CodeAddressCheckUnsupported, Status: http.StatusNotImplemented, Message: "The carrier offers no address check"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := uc.AddressChecker.CheckAddress(ctx, in)
	if errors.Is(err, carrier.ErrAddressCheckUnsupported) {
		return nil, &apperror.Error{Code: CodeAddressCheckUnsupported, Status: http.StatusNotImplemented, Message: "The carrier offers no address check"}
	}
	if err != nil {
		uc.Log.Warn().Err(err).Msg("carrier_address_check_failed")
		return nil, &apperror.Error{Code: "carrier_unavailable", Status: http.StatusServiceUnavailable, Message: "The carrier could not be reached"}
	}
	if !r.Deliverable && strings.TrimSpace(r.Reason) == "" {
		r.Reason = "The carrier cannot serve this address"
	}
	if len(r.Reason) > 300 {
		r.Reason = r.Reason[:300]
	}
	return &AddressCheck{Deliverable: r.Deliverable, Reason: r.Reason, Reference: r.Reference}, nil
}
