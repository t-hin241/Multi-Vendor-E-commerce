package repository

import (
	"context"
	"errors"
	"time"

	"shopee/backend/services/identity/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrProfileConflict = errors.New("profile version conflict")
var ErrEmailChangeInvalid = errors.New("email change unavailable")

type AccountRepository struct{ Pool *pgxpool.Pool }

func (r AccountRepository) UpdateProfile(ctx context.Context, userID string, p domain.ProfileUpdate) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE users SET full_name=$2,phone=$3,profile_version=profile_version+1,updated_at=now() WHERE id=$1 AND profile_version=$4 AND is_active`, userID, p.FullName, p.Phone, p.ExpectedVersion)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrProfileConflict
	}
	return err
}

func (r AccountRepository) Sessions(ctx context.Context, userID string, before time.Time, beforeID string, limit int) ([]domain.AccountSession, error) {
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT family_id,min(created_at),max(created_at) FROM refresh_tokens WHERE user_id=$1 AND family_id IS NOT NULL
 GROUP BY family_id HAVING bool_or(revoked_at IS NULL AND expires_at>now()) AND ($2::timestamptz IS NULL OR (min(created_at),family_id)<($2,$3))
 ORDER BY min(created_at) DESC,family_id DESC LIMIT $4`, userID, nullableTime(before), beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.AccountSession, 0)
	for rows.Next() {
		var s domain.AccountSession
		if err := rows.Scan(&s.ID, &s.CreatedAt, &s.LastActiveAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// The caller holds the user lock, shared with refresh, reset and login.
func (r AccountRepository) EmailChangeRate(ctx context.Context, userID string) (time.Time, int, error) {
	var last *time.Time
	var count int
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT max(created_at),count(*) FILTER (WHERE created_at>now()-interval '1 hour') FROM email_change_operations WHERE user_id=$1`, userID).Scan(&last, &count)
	if last == nil {
		return time.Time{}, count, err
	}
	return *last, count, err
}

func (r AccountRepository) InvalidateEmailChanges(ctx context.Context, userID string) error {
	q := connection(ctx, r.Pool)
	if _, err := q.Exec(ctx, `UPDATE email_change_operations SET used_at=COALESCE(used_at,now()),target_ciphertext=NULL WHERE user_id=$1`, userID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE password_reset_deliveries SET encrypted_token=NULL,status='expired',lease_until=NULL WHERE user_id=$1 AND kind='email_change' AND encrypted_token IS NOT NULL`, userID)
	return err
}

func (r AccountRepository) IssueEmailChange(ctx context.Context, userID, hash string, target, delivery []byte, version int64, expires time.Time) (string, error) {
	if err := r.InvalidateEmailChanges(ctx, userID); err != nil {
		return "", err
	}
	q := connection(ctx, r.Pool)
	var id string
	if err := q.QueryRow(ctx, `INSERT INTO email_change_operations(user_id,token_hash,target_ciphertext,auth_version,expires_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, userID, hash, target, version, expires).Scan(&id); err != nil {
		return "", err
	}
	_, err := q.Exec(ctx, `INSERT INTO password_reset_deliveries(user_id,email_change_id,kind,encrypted_token,expires_at) VALUES($1,$2,'email_change',$3,$4)`, userID, id, delivery, expires)
	return id, err
}

// Lookup does not lock the operation: all workflows lock its user first.
func (r AccountRepository) EmailChange(ctx context.Context, hash string) (*domain.EmailChange, error) {
	var op domain.EmailChange
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT id,user_id,target_ciphertext,auth_version,expires_at,used_at FROM email_change_operations WHERE token_hash=$1`, hash).Scan(&op.ID, &op.UserID, &op.TargetCiphertext, &op.AuthVersion, &op.ExpiresAt, &op.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEmailChangeInvalid
	}
	return &op, err
}

func (r AccountRepository) CompleteEmailChange(ctx context.Context, op *domain.EmailChange, email string, notice []byte) error {
	q := connection(ctx, r.Pool)
	tag, err := q.Exec(ctx, `UPDATE email_change_operations SET used_at=now(),target_ciphertext=NULL WHERE id=$1 AND used_at IS NULL AND expires_at>now()`, op.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrEmailChangeInvalid
	}
	tag, err = q.Exec(ctx, `UPDATE users SET email=$2,email_verified_at=now(),auth_version=auth_version+1,profile_version=profile_version+1,updated_at=now() WHERE id=$1 AND auth_version=$3 AND is_active`, op.UserID, email, op.AuthVersion)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrEmailChangeInvalid
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrEmailChangeInvalid
	}
	if _, err = q.Exec(ctx, `UPDATE email_verification_tokens SET used_at=COALESCE(used_at,now()) WHERE user_id=$1`, op.UserID); err != nil {
		return err
	}
	if _, err = q.Exec(ctx, `UPDATE password_reset_deliveries SET encrypted_token=NULL,status='expired',lease_until=NULL WHERE user_id=$1 AND kind IN ('email_change','email_verification') AND encrypted_token IS NOT NULL`, op.UserID); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO password_reset_deliveries(user_id,email_change_id,kind,encrypted_token,expires_at) VALUES($1,$2,'email_changed',$3,now()+interval '24 hours')`, op.UserID, op.ID, notice)
	return err
}

// Purge clears expired ciphertext on each sweep. Keep rate-limit history for
// at least an hour; retention is validated by the configuration layer.
func (r AccountRepository) Purge(ctx context.Context, retention time.Duration) error {
	q := connection(ctx, r.Pool)
	if _, err := q.Exec(ctx, `UPDATE email_change_operations SET target_ciphertext=NULL,used_at=COALESCE(used_at,now()) WHERE expires_at<=now() AND target_ciphertext IS NOT NULL`); err != nil {
		return err
	}
	cutoff := time.Now().Add(-retention)
	if _, err := q.Exec(ctx, `DELETE FROM password_reset_deliveries WHERE expires_at<$1 AND (verification_id IS NOT NULL OR email_change_id IS NOT NULL)`, cutoff); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM email_change_operations o WHERE expires_at<$1 AND NOT EXISTS(SELECT 1 FROM password_reset_deliveries d WHERE d.email_change_id=o.id)`, cutoff); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM email_verification_tokens v WHERE expires_at<$1 AND NOT EXISTS(SELECT 1 FROM password_reset_deliveries d WHERE d.verification_id=v.id)`, cutoff)
	return err
}
