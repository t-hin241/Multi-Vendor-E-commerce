package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/identity/internal/usecase"
)

// InternalHandler serves service-to-service lookups — today just Notification
// resolving a user id to an email/name to address a message to. Like the
// other services' internal handlers, it is not proxied by the gateway's
// public route table, so it is reachable only from inside the compose
// network in this MVP topology.
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

	httpresponse.OK(c, http.StatusOK, toUserResponse(user))
}
