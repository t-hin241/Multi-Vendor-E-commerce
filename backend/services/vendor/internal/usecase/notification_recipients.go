package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// recipientPurposes maps a notice purpose (AF-08) to the permission a staff
// member needs to receive it. The owner receives every purpose.
var recipientPurposes = map[string]string{
	"orders":  shopaccess.OrdersFulfill,
	"returns": shopaccess.ReturnsHandle,
	"finance": shopaccess.FinanceRead,
	// PW-009: support cases waiting for the shop.
	"support": shopaccess.SupportReply,
}

// NoticeRecipient is one person who may be told about a shop's work.
type NoticeRecipient struct {
	UserID            string
	Role              string
	MembershipVersion int64
}

// NoticeRecipients answers GET /internal/vendors/:id/notification-recipients.
type NoticeRecipients struct {
	VendorID   string
	Purpose    string
	Recipients []NoticeRecipient
	// PermissionVersion changes whenever the list or a member's grants
	// change, so Notification records which list it used.
	PermissionVersion string
}

// NotificationRecipients lists who may receive a shop notice for purpose
// right now: the owner, and staff holding the purpose's permission (only
// while shop staff is enabled). A person whose account may not act for the
// shop (locked, wrong role) is left out, so a notice never reaches someone
// without access; the list can therefore be empty. Whether staff opted in
// is Notification's decision, not Vendor's.
func (uc *StaffUseCase) NotificationRecipients(ctx context.Context, vendorID, purpose string) (*NoticeRecipients, error) {
	permission, ok := recipientPurposes[purpose]
	if !ok {
		return nil, apperror.Validation("purpose must be orders, returns, finance or support")
	}
	if _, err := uuid.Parse(vendorID); err != nil {
		return nil, apperror.Validation("Invalid shop ID")
	}
	if _, err := uc.Vendors.FindByID(ctx, vendorID); err != nil {
		if errors.Is(err, repository.ErrVendorNotFound) {
			return nil, apperror.NotFound("Shop not found")
		}
		return nil, apperror.Internal(err)
	}
	members, err := uc.Staff.ListMembers(ctx, vendorID)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	out := &NoticeRecipients{VendorID: vendorID, Purpose: purpose, Recipients: []NoticeRecipient{}}
	for _, m := range members {
		if !m.Active() || (m.Role == domain.MemberStaff && !uc.Enabled) || !m.Has(permission) {
			continue
		}
		ok, err := uc.accountMayAct(ctx, m.UserID, m.Role)
		if err != nil {
			return nil, err
		}
		if ok {
			out.Recipients = append(out.Recipients, NoticeRecipient{UserID: m.UserID, Role: m.Role, MembershipVersion: m.Version})
		}
	}
	sort.Slice(out.Recipients, func(i, j int) bool { return out.Recipients[i].UserID < out.Recipients[j].UserID })
	parts := make([]string, 0, len(out.Recipients))
	for _, r := range out.Recipients {
		parts = append(parts, r.UserID+":"+r.Role+":"+strconv.FormatInt(r.MembershipVersion, 10))
	}
	sum := sha256.Sum256([]byte(purpose + "|" + strings.Join(parts, ",")))
	out.PermissionVersion = hex.EncodeToString(sum[:8])
	return out, nil
}
