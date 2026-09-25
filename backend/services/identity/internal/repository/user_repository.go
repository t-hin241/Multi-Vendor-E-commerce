package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/services/identity/internal/domain"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

var ErrEmailTaken = errors.New("repository: email already registered")
var ErrUserNotFound = errors.New("repository: user not found")

func (r *UserRepository) Create(ctx context.Context, u *domain.User) error {
	const query = `
		INSERT INTO users (email, password_hash, full_name, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, is_active, created_at, updated_at`

	err := r.pool.QueryRow(ctx, query, u.Email, u.PasswordHash, u.FullName, u.Role).
		Scan(&u.ID, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrEmailTaken
		}
		return err
	}
	return nil
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	const query = `
		SELECT id, email, password_hash, full_name, role, is_active, created_at, updated_at
		FROM users WHERE lower(email) = lower($1)`

	return scanUser(r.pool.QueryRow(ctx, query, email))
}

func (r *UserRepository) FindByID(ctx context.Context, id string) (*domain.User, error) {
	const query = `
		SELECT id, email, password_hash, full_name, role, is_active, created_at, updated_at
		FROM users WHERE id = $1`

	return scanUser(r.pool.QueryRow(ctx, query, id))
}

func (r *UserRepository) UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error {
	const query = `UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, query, passwordHash, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// List is admin's user directory: role is an exact-match filter (empty =
// every role), q is a substring match against email or full name (empty =
// unfiltered). Newest accounts first.
func (r *UserRepository) List(ctx context.Context, role, q string, limit, offset int) ([]*domain.User, error) {
	const query = `
		SELECT id, email, password_hash, full_name, role, is_active, created_at, updated_at
		FROM users
		WHERE ($1 = '' OR role = $1)
		  AND ($2 = '' OR email ILIKE '%' || $2 || '%' OR full_name ILIKE '%' || $2 || '%')
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`

	rows, err := r.pool.Query(ctx, query, role, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// SetActive flips a user's account status. Login already checks IsActive
// (auth_usecase.go), so deactivating blocks future logins; the caller is
// responsible for also revoking any already-issued session.
func (r *UserRepository) SetActive(ctx context.Context, userID string, isActive bool) error {
	const query = `UPDATE users SET is_active = $1, updated_at = now() WHERE id = $2`
	tag, err := r.pool.Exec(ctx, query, isActive, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FullName, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &u, nil
}
