package transport

import (
	"time"

	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/usecase"
)

type registerRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8,max=72"`
	FullName string `json:"full_name" binding:"required"`
	Role     string `json:"role" binding:"required,oneof=buyer vendor"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type passwordResetRequestBody struct {
	Email string `json:"email" binding:"required,email"`
}

type passwordResetConfirmBody struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8,max=72"`
}

type userResponse struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
	// EmailVerified (PW-022): the account proved it owns Email.
	EmailVerified bool `json:"email_verified"`
}

type authResponse struct {
	User                  userResponse `json:"user"`
	AccessToken           string       `json:"access_token"`
	AccessTokenExpiresAt  time.Time    `json:"access_token_expires_at"`
	RefreshTokenExpiresAt time.Time    `json:"refresh_token_expires_at"`
}

func toUserResponse(u *domain.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, FullName: u.FullName, Role: string(u.Role), EmailVerified: u.EmailVerifiedAt != nil}
}

// adminUserResponse includes account status and creation time for admin listings.
type adminUserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	Role      string    `json:"role"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	// PermissionVersion is the expected_version for grant changes (AF-19).
	PermissionVersion int64 `json:"permission_version"`
}

func toAdminUserResponse(u *domain.User) adminUserResponse {
	return adminUserResponse{
		ID: u.ID, Email: u.Email, FullName: u.FullName, Role: string(u.Role),
		IsActive: u.IsActive, CreatedAt: u.CreatedAt, PermissionVersion: u.PermissionVersion,
	}
}

func toAdminUserResponseList(users []*domain.User) []adminUserResponse {
	out := make([]adminUserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, toAdminUserResponse(u))
	}
	return out
}

type setActiveRequest struct {
	IsActive bool `json:"is_active"`
}

func toAuthResponse(r *usecase.AuthResult) authResponse {
	return authResponse{
		User:                  toUserResponse(r.User),
		AccessToken:           r.AccessToken,
		AccessTokenExpiresAt:  r.AccessTokenExpiresAt,
		RefreshTokenExpiresAt: r.RefreshTokenExpiresAt,
	}
}
