package repository

import (
	"context"
	"time"
)

func (r *PasswordResetRepository) QueueDelivery(ctx context.Context, userID, hash string, encrypted []byte, expires time.Time) error {
	_, err := connection(ctx, r.pool).Exec(ctx, `INSERT INTO password_reset_deliveries(reset_id,user_id,encrypted_token,expires_at) SELECT id,user_id,$3,$4 FROM password_reset_tokens WHERE user_id=$1 AND token_hash=$2`, userID, hash, encrypted, expires)
	return err
}
func (r *PasswordResetRepository) InvalidateForUser(ctx context.Context, userID string) error {
	if _, err := connection(ctx, r.pool).Exec(ctx, `UPDATE password_reset_tokens SET used_at=COALESCE(used_at,now()) WHERE user_id=$1`, userID); err != nil {
		return err
	}
	_, err := connection(ctx, r.pool).Exec(ctx, `UPDATE password_reset_deliveries SET encrypted_token=NULL,status='expired',lease_until=NULL WHERE user_id=$1 AND encrypted_token IS NOT NULL`, userID)
	return err
}

type ResetDelivery struct {
	ID, UserID, Email string
	EncryptedToken    []byte
}

func (r *PasswordResetRepository) ClaimDelivery(ctx context.Context) (string, error) {
	// Clear encrypted material for expired deliveries.
	if _, err := r.pool.Exec(ctx, `UPDATE password_reset_deliveries SET encrypted_token=NULL,status='expired',lease_until=NULL WHERE encrypted_token IS NOT NULL AND expires_at<=now()`); err != nil {
		return "", err
	}
	var id string
	err := r.pool.QueryRow(ctx, `UPDATE password_reset_deliveries SET status='sending',attempts=attempts+1,lease_until=now()+interval '45 seconds' WHERE id=(SELECT id FROM password_reset_deliveries WHERE status IN ('pending','sending') AND encrypted_token IS NOT NULL AND expires_at>now() AND attempts<5 AND next_attempt_at<=now() AND (lease_until IS NULL OR lease_until<now()) ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id`).Scan(&id)
	// Mark exhausted deliveries failed after their worker lease expires.
	if _, cleanupErr := r.pool.Exec(ctx, `UPDATE password_reset_deliveries SET status='failed',encrypted_token=NULL WHERE attempts>=5 AND status='sending' AND lease_until<now()`); cleanupErr != nil {
		return "", cleanupErr
	}
	return id, err
}
func (r *PasswordResetRepository) ReadDelivery(ctx context.Context, id string) (*ResetDelivery, error) {
	d := &ResetDelivery{}
	err := r.pool.QueryRow(ctx, `SELECT d.id,d.user_id,u.email,d.encrypted_token FROM password_reset_deliveries d JOIN password_reset_tokens t ON t.id=d.reset_id JOIN users u ON u.id=d.user_id WHERE d.id=$1 AND d.status='sending' AND d.lease_until>now() AND d.expires_at>now() AND t.used_at IS NULL AND t.expires_at>now() AND u.is_active`, id).Scan(&d.ID, &d.UserID, &d.Email, &d.EncryptedToken)
	return d, err
}
func (r *PasswordResetRepository) FinishDelivery(ctx context.Context, id string, success bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE password_reset_deliveries SET status=CASE WHEN $2 THEN 'sent' WHEN attempts>=5 THEN 'failed' ELSE 'pending' END, encrypted_token=CASE WHEN $2 OR attempts>=5 THEN NULL ELSE encrypted_token END, lease_until=NULL,next_attempt_at=now()+make_interval(secs=>30*attempts) WHERE id=$1 AND status='sending'`, id, success)
	return err
}
