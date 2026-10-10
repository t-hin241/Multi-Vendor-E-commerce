package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/identity/internal/usecase"
)

// InternalHandler resolves account data only for authenticated service callers.
type InternalHandler struct {
	auth *usecase.AuthUseCase
	log  zerolog.Logger
}

func NewInternalHandler(auth *usecase.AuthUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{auth: auth, log: log}
}

func (h *InternalHandler) GetUser(c *gin.Context) {
	user, err := h.auth.Me(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"id": user.ID, "email": user.Email, "full_name": user.FullName, "role": user.Role, "is_active": user.IsActive,
		"email_verified": user.EmailVerifiedAt != nil})
}
