package middleware

import (
	"errors"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
)

const (
	ContextKeyUserID = "auth_user_id"
	ContextKeyRole   = "auth_role"
)

// RequireAuth verifies the bearer access token on every request. It never
// trusts a user id or role coming from a header set by the client or
// gateway — the only source of truth is a token signed with the shared
// secret.
func RequireAuth(manager *authjwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		tokenString, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || tokenString == "" {
			httpresponse.Error(c, 401, "unauthorized", "Missing bearer token")
			c.Abort()
			return
		}

		claims, err := manager.Parse(tokenString)
		if err == nil {
			err = manager.VerifySession(c.Request.Context(), claims)
		}
		if err != nil {
			if errors.Is(err, authjwt.ErrVerificationUnavailable) {
				httpresponse.Error(c, 503, "service_unavailable", "Session verification temporarily unavailable")
				c.Abort()
				return
			}
			httpresponse.Error(c, 401, "unauthorized", "Invalid or expired token")
			c.Abort()
			return
		}

		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyRole, claims.Role)
		c.Next()
	}
}

// OptionalAuth identifies the caller when a valid bearer token is present,
// without requiring one — for endpoints that serve anonymous visitors but
// adjust their response for a known caller (e.g. the public product page
// showing exact stock to the product's own vendor or an admin). A missing or
// invalid token is never an error here: the request simply proceeds as
// anonymous, the same as if this middleware weren't present.
func OptionalAuth(manager *authjwt.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		tokenString, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || tokenString == "" {
			c.Next()
			return
		}

		claims, err := manager.Parse(tokenString)
		if err == nil {
			err = manager.VerifySession(c.Request.Context(), claims)
		}
		if err != nil {
			c.Next()
			return
		}

		c.Set(ContextKeyUserID, claims.UserID)
		c.Set(ContextKeyRole, claims.Role)
		c.Next()
	}
}

// RequireRole must run after RequireAuth. It rejects requests whose token
// role is not one of the allowed roles.
func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		if !slices.Contains(roles, role) {
			httpresponse.Error(c, 403, "forbidden", "You do not have permission to perform this action")
			c.Abort()
			return
		}
		c.Next()
	}
}

func GetUserID(c *gin.Context) string {
	v, _ := c.Get(ContextKeyUserID)
	s, _ := v.(string)
	return s
}

func GetRole(c *gin.Context) string {
	v, _ := c.Get(ContextKeyRole)
	s, _ := v.(string)
	return s
}

// ContextKeyAccountRole keeps the account's own role when a route makes
// the caller act in another capacity (see SellerConsole).
const ContextKeyAccountRole = "auth_account_role"

// SellerConsole must run after RequireAuth on seller routes whose every
// use case checks a shop permission with Vendor (AF-17). A shop member may
// hold a buyer or a vendor account; on these routes either acts as
// "vendor", so handlers keep one seller code path. Shop membership, not
// this role, is what grants access.
func SellerConsole() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := GetRole(c)
		if role != "vendor" && role != "buyer" {
			httpresponse.Error(c, 403, "forbidden", "You do not have permission to perform this action")
			c.Abort()
			return
		}
		c.Set(ContextKeyAccountRole, role)
		c.Set(ContextKeyRole, "vendor")
		c.Next()
	}
}

// GetAccountRole is the account's own role, even on SellerConsole routes.
func GetAccountRole(c *gin.Context) string {
	if v, ok := c.Get(ContextKeyAccountRole); ok {
		s, _ := v.(string)
		return s
	}
	return GetRole(c)
}
