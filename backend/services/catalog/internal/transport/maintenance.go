package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/catalog/internal/usecase"
)

type MaintenanceHandler struct {
	Service usecase.Maintenance
	Log     zerolog.Logger
}

func (h MaintenanceHandler) Stats(c *gin.Context) {
	stats, err := h.Service.Stats(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, 200, stats)
}
func (h MaintenanceHandler) Replay(c *gin.Context) {
	var req struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", "Invalid replay request")
		return
	}
	if err := h.Service.Replay(c.Request.Context(), middleware.GetUserID(c), req.Kind, req.ID, req.Reason); err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, 202, gin.H{"queued": true})
}
