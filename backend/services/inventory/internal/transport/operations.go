package transport

import (
	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/inventory/internal/usecase"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func RegisterOperations(r *gin.Engine, jwt *authjwt.Manager, m usecase.Maintenance, log zerolog.Logger) {
	group := r.Group("/api/inventory/admin/operations", middleware.RequireAuth(jwt), middleware.RequireRole("admin"))
	group.GET("", func(c *gin.Context) {
		v, err := m.Stats(c.Request.Context(), middleware.GetUserID(c))
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		httpresponse.OK(c, 200, v)
	})
	group.GET("/issues", func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if limit < 1 || limit > 100 || offset < 0 || offset > 10000 {
			httpresponse.Error(c, 400, "validation_error", "Invalid pagination")
			return
		}
		v, err := m.Issues(c.Request.Context(), middleware.GetUserID(c), limit, offset)
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		httpresponse.OK(c, 200, v)
	})
	group.POST("/repair", func(c *gin.Context) {
		var req struct {
			OrderID string `json:"order_id" binding:"required,uuid"`
			Action  string `json:"action" binding:"required"`
			Reason  string `json:"reason" binding:"required,max=500"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			httpresponse.Error(c, 400, "validation_error", "Invalid operation repair request")
			return
		}
		if err := m.Repair(c.Request.Context(), middleware.GetUserID(c), req.OrderID, req.Action, req.Reason); err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		httpresponse.OK(c, 200, gin.H{"accepted": true})
	})
}
