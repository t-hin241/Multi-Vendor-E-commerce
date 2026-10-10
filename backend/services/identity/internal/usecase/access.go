package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/repository"
)

// AccessStore stores admin grants and reauthentication proofs.
type AccessStore interface {
	ActiveBundles(ctx context.Context, userID string) (*domain.User, []string, error)
	LockSubject(ctx context.Context, userID string) (*domain.User, error)
	ListGrants(ctx context.Context, userID string, includeRevoked bool, limit int) ([]*domain.PermissionGrant, error)
	FindGrant(ctx context.Context, id string) (*domain.PermissionGrant, error)
	Grant(ctx context.Context, userID, bundle, grantedBy, reason string, expectedVersion int64) (*domain.PermissionGrant, int64, error)
	Revoke(ctx context.Context, grantID, userID, revokedBy, reason string, expectedVersion int64) (int64, error)
	HasActiveBundleHolder(ctx context.Context, bundle string) (bool, error)
	FindAdminByEmail(ctx context.Context, email string) (*domain.User, error)
	CreateProof(ctx context.Context, proofHash []byte, userID, purpose, operationHash string, expiresAt time.Time) error
	ConsumeProof(ctx context.Context, proofHash []byte, userID, purpose, operationHash string, now time.Time) error
}

// AccessUseCase owns scoped admin permissions (AF-19): which bundles an
// admin holds, who may change that, and recent-reauthentication proofs.
type AccessUseCase struct {
	Store AccessStore
	Users UserRepository
	Tx    Transactions
	// Scoped is FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED. Off, every active
	// admin holds every bundle, as before AF-19; grants can still be
	// prepared so the switch is safe.
	Scoped bool
	Now    func() time.Time
	// SecondFactor (PW-028), when MFARequired (FEATURE_ADMIN_MFA_REQUIRED),
	// checks the authenticator code of every reauthentication.
	SecondFactor interface {
		Require(ctx context.Context, userID, code string) error
	}
	MFARequired bool
}

func (uc *AccessUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

func accessError(status int, code apperror.Code, message string) *apperror.Error {
	return &apperror.Error{Code: code, Message: message, Status: status}
}

// Effective returns the bundles an active admin may use now and its
// permission version; nil for anyone else.
func (uc *AccessUseCase) Effective(ctx context.Context, userID string) ([]string, int64, error) {
	u, grants, err := uc.Store.ActiveBundles(ctx, userID)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, apperror.Internal(err)
	}
	if !u.IsActive || u.Role != domain.RoleAdmin {
		return nil, u.PermissionVersion, nil
	}
	if !uc.Scoped {
		return adminaccess.Bundles(), u.PermissionVersion, nil
	}
	return grants, u.PermissionVersion, nil
}

// Check answers the internal permission question for one bundle.
func (uc *AccessUseCase) Check(ctx context.Context, userID, bundle string) (bool, int64, error) {
	if !adminaccess.Known(bundle) {
		return false, 0, apperror.Validation("Unknown permission bundle")
	}
	held, version, err := uc.Effective(ctx, userID)
	if err != nil {
		return false, 0, err
	}
	return slices.Contains(held, bundle), version, nil
}

// require refuses an actor without bundle (deny by default).
func (uc *AccessUseCase) require(ctx context.Context, actorID, bundle string) error {
	ok, _, err := uc.Check(ctx, actorID, bundle)
	if err != nil {
		return err
	}
	if !ok {
		return adminaccess.Missing(bundle)
	}
	return nil
}

// AdminAccess is one admin with its grants, for the access screen.
type AdminAccess struct {
	User   *domain.User
	Grants []*domain.PermissionGrant
}

// ListGrants lists grants for an access manager; subjectID filters.
func (uc *AccessUseCase) ListGrants(ctx context.Context, actorID, subjectID string, includeRevoked bool) ([]*domain.PermissionGrant, error) {
	if err := uc.require(ctx, actorID, adminaccess.AccessManage); err != nil {
		return nil, err
	}
	out, err := uc.Store.ListGrants(ctx, subjectID, includeRevoked, 500)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return out, nil
}

var (
	errSelfGrant      = accessError(http.StatusConflict, "self_approval", "You cannot change your own permissions; ask another access manager")
	errVersionStale   = accessError(http.StatusConflict, "stale_snapshot", "This admin's permissions changed meanwhile; reload and try again")
	errGrantExists    = accessError(http.StatusConflict, "already_granted", "This admin already holds this permission")
	errNotAdminTarget = apperror.Validation("Permissions can only be granted to an active admin account")
)

// Grant gives an admin a bundle. Nobody changes their own grants, and the
// change applies only to the version the manager looked at.
func (uc *AccessUseCase) Grant(ctx context.Context, actorID, subjectID, bundle, reason string, expectedVersion int64) (*domain.PermissionGrant, error) {
	reason, err := domain.ValidateGrant(bundle, reason)
	if err != nil {
		return nil, err
	}
	if actorID == subjectID {
		return nil, errSelfGrant
	}
	var grant *domain.PermissionGrant
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.require(ctx, actorID, adminaccess.AccessManage); err != nil {
			return err
		}
		subject, err := uc.Store.LockSubject(ctx, subjectID)
		if errors.Is(err, repository.ErrUserNotFound) {
			return errNotAdminTarget
		}
		if err != nil {
			return err
		}
		if subject.Role != domain.RoleAdmin || !subject.IsActive {
			return errNotAdminTarget
		}
		if subject.PermissionVersion != expectedVersion {
			return errVersionStale
		}
		grant, _, err = uc.Store.Grant(ctx, subjectID, bundle, actorID, reason, expectedVersion)
		switch {
		case errors.Is(err, repository.ErrGrantExists):
			return errGrantExists
		case errors.Is(err, repository.ErrPermissionVersion):
			return errVersionStale
		case err != nil:
			return err
		}
		return uc.Users.Audit(ctx, actorID, subjectID, "permission_granted", "bundle="+bundle+"; "+reason)
	})
	if err != nil {
		return nil, wrapAccess(err)
	}
	return grant, nil
}

// Revoke ends a grant; the next request needing it is refused.
func (uc *AccessUseCase) Revoke(ctx context.Context, actorID, grantID, reason string, expectedVersion int64) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return apperror.Validation("A reason of 1-500 characters is required")
	}
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.require(ctx, actorID, adminaccess.AccessManage); err != nil {
			return err
		}
		g, err := uc.Store.FindGrant(ctx, grantID)
		if errors.Is(err, repository.ErrGrantNotFound) {
			return apperror.NotFound("Grant not found")
		}
		if err != nil {
			return err
		}
		if g.UserID == actorID {
			return errSelfGrant
		}
		if g.Status != domain.GrantActive {
			return accessError(http.StatusConflict, "already_revoked", "This grant is no longer active")
		}
		subject, err := uc.Store.LockSubject(ctx, g.UserID)
		if err != nil {
			return err
		}
		if subject.PermissionVersion != expectedVersion {
			return errVersionStale
		}
		if _, err := uc.Store.Revoke(ctx, grantID, g.UserID, actorID, reason, expectedVersion); err != nil {
			if errors.Is(err, repository.ErrPermissionVersion) {
				return errVersionStale
			}
			return err
		}
		return uc.Users.Audit(ctx, actorID, g.UserID, "permission_revoked", "bundle="+g.Bundle+"; "+reason)
	})
	return wrapAccess(err)
}

func wrapAccess(err error) error {
	if err == nil {
		return nil
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return app
	}
	return apperror.Internal(err)
}

// BootstrapAccessManager grants access.manage to an existing active admin
// when nobody holds it yet. It is the audited way out of the empty state;
// it never creates an account and refuses once an access manager exists.
func (uc *AccessUseCase) BootstrapAccessManager(ctx context.Context, email, operator, reason string) error {
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(operator) == "" || reason == "" || len(reason) > 400 {
		return apperror.Validation("Operator and reason are required")
	}
	return uc.Tx.Run(ctx, func(ctx context.Context) error {
		exists, err := uc.Store.HasActiveBundleHolder(ctx, adminaccess.AccessManage)
		if err != nil {
			return err
		}
		if exists {
			return accessError(http.StatusConflict, "bootstrap_refused", "An access manager already exists; use the access screen")
		}
		u, err := uc.Store.FindAdminByEmail(ctx, domain.NormalizeEmail(email))
		if err != nil || u.Role != domain.RoleAdmin || !u.IsActive {
			return errNotAdminTarget
		}
		if _, _, err := uc.Store.Grant(ctx, u.ID, adminaccess.AccessManage, "", "bootstrap by "+operator+": "+reason, u.PermissionVersion); err != nil {
			return err
		}
		return uc.Users.Audit(ctx, "", u.ID, "access_bootstrap", "bundle=access.manage; operator="+operator+"; "+reason)
	})
}

// Proof is a recent-reauthentication proof. It is shown once and only its
// hash is stored.
type Proof struct {
	Value     string
	ExpiresAt time.Time
}

// Reauthenticate checks the admin's current password and returns a proof
// bound to purpose and operation. It is a password re-check, not MFA.
func (uc *AccessUseCase) Reauthenticate(ctx context.Context, userID, password, purpose, operationHash string, otpCode ...string) (*Proof, error) {
	if err := domain.ValidateProofRequest(purpose, operationHash); err != nil {
		return nil, err
	}
	user, err := uc.Users.FindByID(ctx, userID)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, apperror.Unauthorized("Password is incorrect")
	}
	if err != nil {
		return nil, apperror.Internal(err)
	}
	if !user.IsActive || user.Role != domain.RoleAdmin {
		return nil, apperror.Forbidden("Admin access required")
	}
	// bcrypt runs outside any transaction (no connection held).
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, apperror.Unauthorized("Password is incorrect")
	}
	// PW-028: the authenticator app is the second factor.
	if uc.MFARequired {
		if uc.SecondFactor == nil {
			return nil, apperror.Internal(errors.New("second factor is required but not configured"))
		}
		code := ""
		if len(otpCode) > 0 {
			code = strings.TrimSpace(otpCode[0])
		}
		if err := uc.SecondFactor.Require(ctx, userID, code); err != nil {
			return nil, err
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, apperror.Internal(err)
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(value))
	expires := uc.now().Add(domain.ProofTTL)
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Store.CreateProof(ctx, sum[:], userID, purpose, operationHash, expires); err != nil {
			return err
		}
		return uc.Users.Audit(ctx, userID, userID, "reauthenticated", "purpose="+purpose)
	})
	if err != nil {
		return nil, apperror.Internal(err)
	}
	return &Proof{Value: value, ExpiresAt: expires}, nil
}

// ConsumeProof spends a proof for the service performing the operation.
func (uc *AccessUseCase) ConsumeProof(ctx context.Context, proof, userID, purpose, operationHash string) error {
	if err := domain.ValidateProofRequest(purpose, operationHash); err != nil || strings.TrimSpace(proof) == "" {
		return adminaccess.ReauthRequired()
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(proof)))
	err := uc.Store.ConsumeProof(ctx, sum[:], userID, purpose, operationHash, uc.now())
	if errors.Is(err, repository.ErrProofInvalid) {
		return adminaccess.ReauthRequired()
	}
	if err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// Require is the guard check for Identity's own admin routes.
func (uc *AccessUseCase) Require(ctx context.Context, userID, bundle string) (int64, error) {
	ok, version, err := uc.Check(ctx, userID, bundle)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, adminaccess.Missing(bundle)
	}
	return version, nil
}

// ListSubjects lists admin accounts with their active grants for an
// access manager.
func (uc *AccessUseCase) ListSubjects(ctx context.Context, actorID string) ([]AdminAccess, error) {
	if err := uc.require(ctx, actorID, adminaccess.AccessManage); err != nil {
		return nil, err
	}
	admins, err := uc.Users.List(ctx, string(domain.RoleAdmin), "", 100, 0)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	grants, err := uc.Store.ListGrants(ctx, "", false, 1000)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	out := make([]AdminAccess, 0, len(admins))
	for _, a := range admins {
		item := AdminAccess{User: a, Grants: []*domain.PermissionGrant{}}
		for _, g := range grants {
			if g.UserID == a.ID {
				item.Grants = append(item.Grants, g)
			}
		}
		out = append(out, item)
	}
	return out, nil
}
