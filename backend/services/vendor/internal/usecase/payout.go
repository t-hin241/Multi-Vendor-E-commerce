package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/vendorsvc/internal/domain"
)

type PayoutRepository interface {
	NextVersion(context.Context, string) (int64, error)
	Create(context.Context, *domain.PayoutAccount) error
	Find(context.Context, string, string, int64) (*domain.PayoutAccount, error)
	FindDefaultVerified(context.Context, string) (*domain.PayoutAccount, error)
	List(context.Context, string, int, int) ([]*domain.PayoutAccount, error)
	Decide(context.Context, *domain.PayoutAccount, string, string, string) error
	AuditRead(context.Context, string, string, string, string) error
}
type PayoutCipher interface {
	Encrypt(string, string) ([]byte, error)
	Decrypt([]byte, string) (string, error)
}
type PayoutUseCase struct {
	Accounts PayoutRepository
	Vendors  VendorRepositoryPort
	Audit    AuditLogRepositoryPort
	Ops      Operations
	Cipher   PayoutCipher
	// Proofs and RequireProof (FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED,
	// AF-19): verifying a destination and reading its full details need a
	// fresh password confirmation for exactly that account version.
	Proofs       ProofConsumer
	RequireProof bool
}

// ProofConsumer spends a recent-reauthentication proof (Identity).
type ProofConsumer interface {
	ConsumeProof(ctx context.Context, proof, userID, purpose, operationHash string) error
}

// Reauthentication purposes and operation references the admin console
// confirms the password for.
const (
	ProofPurposePayoutDecide  = "vendor.payout.decide"
	ProofPurposePayoutDetails = "vendor.payout.details"
)

// PayoutDecisionRef is the operation a decision proof is bound to.
func PayoutDecisionRef(accountID string, version int64, verify bool) string {
	action := "reject"
	if verify {
		action = "verify"
	}
	return fmt.Sprintf("payout_account:%s:v%d:%s", accountID, version, action)
}

// PayoutDetailsRef is the operation a details proof is bound to.
func PayoutDetailsRef(accountID string, version int64) string {
	return fmt.Sprintf("payout_account:%s:v%d:details", accountID, version)
}

func (u *PayoutUseCase) checkProof(ctx context.Context, actor, purpose, ref, proof string) error {
	if !u.RequireProof {
		return nil
	}
	if u.Proofs == nil {
		return apperror.Internal(errors.New("reauthentication is not configured"))
	}
	return u.Proofs.ConsumeProof(ctx, proof, actor, purpose, ref)
}

// DecideWithProof is Decide behind the admin's password confirmation.
func (u *PayoutUseCase) DecideWithProof(ctx context.Context, actor, vendor, id string, version int64, verify bool, reason, proof string) (*domain.PayoutAccount, error) {
	if err := u.Ops.Actors.RequireRole(ctx, actor, "admin"); err != nil {
		return nil, err
	}
	if err := u.checkProof(ctx, actor, ProofPurposePayoutDecide, PayoutDecisionRef(id, version, verify), proof); err != nil {
		return nil, err
	}
	return u.Decide(ctx, actor, vendor, id, version, verify, reason)
}

// DetailsWithProof is the admin's Details behind a password confirmation.
func (u *PayoutUseCase) DetailsWithProof(ctx context.Context, actor, vendor, id string, version int64, purpose, proof string) (*domain.PayoutDetails, error) {
	if err := u.Ops.Actors.RequireRole(ctx, actor, "admin"); err != nil {
		return nil, err
	}
	if err := u.checkProof(ctx, actor, ProofPurposePayoutDetails, PayoutDetailsRef(id, version), proof); err != nil {
		return nil, err
	}
	return u.Details(ctx, actor, vendor, id, version, purpose, false)
}

func payoutAAD(a *domain.PayoutAccount, field string) string {
	return fmt.Sprintf("vendor-payout:%s:%s:%d:%s", a.VendorID, a.ID, a.Version, field)
}
func (u *PayoutUseCase) Submit(ctx context.Context, user, vendor, bank, number, name string) (out *domain.PayoutAccount, err error) {
	if err = u.Ops.Actors.RequireRole(ctx, user, "vendor"); err != nil {
		return
	}
	bank, number, name = strings.TrimSpace(bank), strings.TrimSpace(number), strings.TrimSpace(name)
	if err = domain.ValidatePayout(bank, number, name); err != nil {
		return
	}
	err = u.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		if _, e := getOwnedVendor(ctx, u.Vendors, user, vendor); e != nil {
			return e
		}
		version, e := u.Accounts.NextVersion(ctx, vendor)
		if e != nil {
			return e
		}
		a := &domain.PayoutAccount{ID: uuid.NewString(), VendorID: vendor, Version: version, BankBIN: bank, Last4: number[len(number)-4:], Status: "pending"}
		a.NumberCipher, e = u.Cipher.Encrypt(number, payoutAAD(a, "number"))
		if e != nil {
			return e
		}
		a.NameCipher, e = u.Cipher.Encrypt(name, payoutAAD(a, "name"))
		if e != nil {
			return e
		}
		if e = u.Accounts.Create(ctx, a); e != nil {
			return e
		}
		if e = u.Audit.Create(ctx, vendor, user, "payout_submitted", nil, version); e != nil {
			return e
		}
		out = a
		return nil
	})
	return out, wrap(err)
}
func (u *PayoutUseCase) List(ctx context.Context, user, vendor string, admin bool, limit, offset int) ([]*domain.PayoutAccount, error) {
	role := "vendor"
	if admin {
		role = "admin"
	}
	if err := u.Ops.Actors.RequireRole(ctx, user, role); err != nil {
		return nil, err
	}
	if !admin {
		if _, err := getOwnedVendor(ctx, u.Vendors, user, vendor); err != nil {
			return nil, err
		}
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, apperror.Validation("Invalid pagination")
	}
	out, err := u.Accounts.List(ctx, vendor, limit, offset)
	return out, wrap(err)
}
func (u *PayoutUseCase) Decide(ctx context.Context, actor, vendor, id string, version int64, verify bool, reason string) (out *domain.PayoutAccount, err error) {
	if err = u.Ops.Actors.RequireRole(ctx, actor, "admin"); err != nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if len(reason) < 2 || len(reason) > 1000 {
		return nil, apperror.Validation("Verification evidence or rejection reason is required (2 to 1000 bytes)")
	}
	err = u.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		v, e := u.Vendors.FindByID(ctx, vendor)
		if e != nil {
			return e
		}
		if v.UserID == actor {
			return apperror.Forbidden("Cannot verify your own payout account")
		}
		a, e := u.Accounts.Find(ctx, vendor, id, version)
		if errors.Is(e, pgx.ErrNoRows) {
			return apperror.NotFound("Account version not found")
		}
		if e != nil {
			return e
		}
		if a.Status != "pending" {
			return apperror.Conflict("Only pending account versions can be reviewed")
		}
		status := "rejected"
		if verify {
			status = "verified"
		}
		if e = u.Accounts.Decide(ctx, a, actor, status, reason); e != nil {
			return e
		}
		if e = u.Audit.Create(ctx, vendor, actor, "payout_"+status, &reason, version); e != nil {
			return e
		}
		out, e = u.Accounts.Find(ctx, vendor, id, version)
		return e
	})
	return out, wrap(err)
}

// Details releases plaintext only after recording the authorized access.
// PayoutDestination is a verified payout account without its number or
// holder name: what Payment needs to reference it in a payout.
type PayoutDestination struct {
	AccountID string `json:"account_id"`
	Version   int64  `json:"version"`
	BankBIN   string `json:"bank_bin"`
	Last4     string `json:"last4"`
}

// DefaultDestination returns a vendor's verified default payout account,
// masked, for Payment's payout batches (service-to-service only).
func (u *PayoutUseCase) DefaultDestination(ctx context.Context, vendor string) (*PayoutDestination, error) {
	a, err := u.Accounts.FindDefaultVerified(ctx, vendor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("No verified payout destination")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return &PayoutDestination{AccountID: a.ID, Version: a.Version, BankBIN: a.BankBIN, Last4: a.Last4}, nil
}

func (u *PayoutUseCase) Details(ctx context.Context, actor, vendor, id string, version int64, purpose string, payment bool) (out *domain.PayoutDetails, err error) {
	if !payment {
		if err = u.Ops.Actors.RequireRole(ctx, actor, "admin"); err != nil {
			return
		}
	}
	purpose = strings.TrimSpace(purpose)
	if len(purpose) < 2 || len(purpose) > 200 {
		return nil, apperror.Validation("An access purpose of 2 to 200 bytes is required")
	}
	err = u.Ops.Tx.Run(ctx, func(ctx context.Context) error {
		if _, e := u.Vendors.FindByID(ctx, vendor); e != nil {
			return e
		}
		a, e := u.Accounts.Find(ctx, vendor, id, version)
		if errors.Is(e, pgx.ErrNoRows) {
			return apperror.NotFound("Account version not found")
		}
		if e != nil {
			return e
		}
		if payment && a.Status != "verified" {
			return apperror.Conflict("Payout destination is not verified; review required")
		}
		number, e := u.Cipher.Decrypt(a.NumberCipher, payoutAAD(a, "number"))
		if e != nil {
			return e
		}
		name, e := u.Cipher.Decrypt(a.NameCipher, payoutAAD(a, "name"))
		if e != nil {
			return e
		}
		scope := "admin-verification"
		if payment {
			scope = "payment-payout"
			actor = ""
		}
		if e = u.Accounts.AuditRead(ctx, id, actor, scope, purpose); e != nil {
			return e
		}
		out = &domain.PayoutDetails{AccountID: id, Version: version, BankBIN: a.BankBIN, Number: number, Name: name}
		return nil
	})
	return out, wrap(err)
}
