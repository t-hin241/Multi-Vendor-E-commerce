package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/catalog/internal/usecase"
)

// StorefrontHandler serves the public, unauthenticated product browsing
// endpoints. Only approved, active products from approved vendors are ever
// returned here.
type StorefrontHandler struct {
	products *usecase.ProductUseCase
	log      zerolog.Logger
}

func NewStorefrontHandler(products *usecase.ProductUseCase, log zerolog.Logger) *StorefrontHandler {
	return &StorefrontHandler{products: products, log: log}
}

func (h *StorefrontHandler) List(c *gin.Context) {
	limit, offset := paginationParams(c)

	products, total, images, vendorNames, quantitySold, vendorInfoDegraded, salesInfoDegraded, err :=
		h.products.ListStorefront(c.Request.Context(), c.Query("category_id"), c.Query("vendor_id"), c.Query("q"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toStorefrontListResponse(products, total, images, vendorNames, quantitySold, vendorInfoDegraded, salesInfoDegraded))
}

func (h *StorefrontHandler) GetBySlug(c *gin.Context) {
	p, images, media, attributeValues, variants, plainStockQuantity, stockInfoDegraded, vendorName, err :=
		h.products.GetPublicBySlug(c.Request.Context(), c.Param("id"), middleware.GetUserID(c), middleware.GetRole(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponseWithImagesAndMedia(p, images, media, attributeValues, variants, plainStockQuantity, stockInfoDegraded, vendorName))
}
