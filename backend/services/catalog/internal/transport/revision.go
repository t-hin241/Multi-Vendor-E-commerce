package transport

import (
	"github.com/gin-gonic/gin"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

func (h *ProductHandler) UpdateContent(c *gin.Context) {
	var req struct {
		Name        string                  `json:"name" binding:"required,max=300"`
		Description string                  `json:"description" binding:"max=50000"`
		Price       int64                   `json:"price_amount" binding:"required,gt=0"`
		Version     int64                   `json:"version" binding:"required,gt=0"`
		Attributes  []attributeValueRequest `json:"attributes" binding:"max=100,dive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", "Invalid product content")
		return
	}
	p, err := h.products.UpdateContent(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Name, req.Description, req.Price, req.Version, toAttributeValueInputs(req.Attributes))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := 200
	if p.EnforcedVersion < p.Version {
		status = 202
	}
	httpresponse.OK(c, status, toProductResponse(p))
}

func (h *ProductHandler) GetForOwner(c *gin.Context) {
	p, values, pkg, err := h.products.GetForOwner(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, gin.H{"product": toProductResponse(p), "attributes": toAttributeValueResponseList(values), "packaging": gin.H{"pkg_weight": pkg.WeightGrams, "pkg_length": pkg.LengthMM, "pkg_width": pkg.WidthMM, "pkg_height": pkg.HeightMM}})
}
