package transport

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/identity/internal/usecase"
)

type AdminHandler struct {
	admin *usecase.AdminUseCase
	log   zerolog.Logger
}

func NewAdminHandler(admin *usecase.AdminUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{admin: admin, log: log}
}

func (h *AdminHandler) ListUsers(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)

	users, err := h.admin.ListUsers(c.Request.Context(), c.Query("role"), c.Query("q"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toAdminUserResponseList(users))
}

func (h *AdminHandler) SetActive(c *gin.Context) {
	var req setActiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	user, err := h.admin.SetActive(c.Request.Context(), c.Param("id"), req.IsActive)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toAdminUserResponse(user))
}

func parseIntDefault(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || v > max {
		return fallback
	}
	return v
}
