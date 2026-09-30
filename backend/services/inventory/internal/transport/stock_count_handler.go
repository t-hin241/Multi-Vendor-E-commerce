package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

type stockCountRequest struct {
	CountID       string `json:"count_id" binding:"required,uuid"`
	CountedOnHand *int64 `json:"counted_on_hand" binding:"required,min=0"`
	Reason        string `json:"reason" binding:"required,max=500"`
}

// RecordStockCount: POST /api/inventory/items/:itemID/stock-counts. The
// vendor reports how many units are physically on hand (including units
// held for pending orders); only a decrease of available stock is
// accepted. 201 for a new count, 200 when the same count_id is retried.
func (h *ItemHandler) RecordStockCount(c *gin.Context) {
	itemID := c.Param("itemID")
	if _, err := uuid.Parse(itemID); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid stock item id")
		return
	}
	var req stockCountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"count_id must be a valid id, counted_on_hand a non-negative integer and reason at most 500 characters")
		return
	}
	count, replayed, err := h.inventory.RecordStockCount(c.Request.Context(), middleware.GetUserID(c), itemID, req.CountID, *req.CountedOnHand, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, count)
}
