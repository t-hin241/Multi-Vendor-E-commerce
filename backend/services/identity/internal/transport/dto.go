package transport

import (
	"time"

	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/usecase"
)

type registerRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	FullName string `json:"full_name" binding:"required"`
	Role     string `json:"role" binding:"required,oneof=buyer vendor"`
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type passwordResetRequestBody struct {
	Email string `json:"email" binding:"required,email"`
}

type passwordResetConfirmBody struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

type userResponse struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
}

type authResponse struct {
	User                  userResponse `json:"user"`
	AccessToken           string       `json:"access_token"`
	AccessTokenExpiresAt  time.Time    `json:"access_token_expires_at"`
	RefreshToken          string       `json:"refresh_token"`
	RefreshTokenExpiresAt time.Time    `json:"refresh_token_expires_at"`
}

func toUserResponse(u *domain.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, FullName: u.FullName, Role: string(u.Role)}
}

func toAuthResponse(r *usecase.AuthResult) authResponse {
	return authResponse{
		User:                  toUserResponse(r.User),
		AccessToken:           r.AccessToken,
		AccessTokenExpiresAt:  r.AccessTokenExpiresAt,
		RefreshToken:          r.RefreshToken,
		RefreshTokenExpiresAt: r.RefreshTokenExpiresAt,
	}
}
