package transport

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/catalog/internal/usecase"
)

const maxUploadBytes = 6 * 1024 * 1024 // slightly above the 5MB domain limit so the error is a clean validation message, not a truncated read

const maxMediaUploadBytes = 21 * 1024 * 1024 // slightly above the 20MB video ceiling, for the same reason

type ProductHandler struct {
	products *usecase.ProductUseCase
	log      zerolog.Logger
}

func NewProductHandler(products *usecase.ProductUseCase, log zerolog.Logger) *ProductHandler {
	return &ProductHandler{products: products, log: log}
}

func (h *ProductHandler) Create(c *gin.Context) {
	var req createProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	p, err := h.products.Create(c.Request.Context(), middleware.GetUserID(c), req.VendorID, req.CategoryID, req.Name, req.Description, req.PriceAmount, toAttributeValueInputs(req.Attributes))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toProductResponse(p))
}

func (h *ProductHandler) ListMine(c *gin.Context) {
	limit, offset := paginationParams(c)

	products, err := h.products.ListMine(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponseList(products))
}

// Submit moves a draft product to pending_review, once the vendor has
// supplied everything admin needs to decide (image + initial stock).
func (h *ProductHandler) Submit(c *gin.Context) {
	p, err := h.products.SubmitForReview(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponse(p))
}

func (h *ProductHandler) SetActive(c *gin.Context) {
	var req setActiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	p, err := h.products.SetActive(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.IsActive)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductResponse(p))
}

func (h *ProductHandler) UploadImage(c *gin.Context) {
	fileHeader, err := c.FormFile("image")
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "An 'image' file field is required")
		return
	}
	if fileHeader.Size > maxUploadBytes {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Image must be no larger than 5MB")
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	img, err := h.products.UploadImage(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), contentType, data)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toProductImageResponse(img))
}

// DeleteImage removes a product's main image entirely (no replacement) —
// the frontend's corner "×" control on an already-uploaded image.
func (h *ProductHandler) DeleteImage(c *gin.Context) {
	if err := h.products.DeleteImage(c.Request.Context(), middleware.GetUserID(c), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"deleted": true})
}

// ListImages returns the vendor's own view of a product's single main
// image, mirroring ListMedia, so the console can preview it right after
// upload.
func (h *ProductHandler) ListImages(c *gin.Context) {
	images, err := h.products.ListImagesForOwner(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductImageResponseList(images))
}

// UploadMedia adds one item to a product's extended-description media
// gallery (images or short videos) — a separate feature from UploadImage's
// plain photo gallery, with its own field name and size ceiling.
func (h *ProductHandler) UploadMedia(c *gin.Context) {
	fileHeader, err := c.FormFile("media")
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A 'media' file field is required")
		return
	}
	if fileHeader.Size > maxMediaUploadBytes {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Media must be no larger than 20MB")
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxMediaUploadBytes+1))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	contentType := fileHeader.Header.Get("Content-Type")
	m, err := h.products.UploadMedia(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), contentType, data)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toProductMediaResponse(m))
}

func (h *ProductHandler) ListMedia(c *gin.Context) {
	media, err := h.products.ListMediaForOwner(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toProductMediaResponseList(media))
}

// CreateVariant adds one option-combination variant (e.g. Color=Red,
// Size=L) to a product the caller owns, with its own SKU — Inventory later
// tracks stock against the returned variant id, independently of the
// product's own (unused, once variants exist) stock record.
func (h *ProductHandler) CreateVariant(c *gin.Context) {
	var req createVariantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	v, details, err := h.products.CreateVariant(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.SKU, req.OptionIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toVariantResponse(v, details))
}

func (h *ProductHandler) ListVariants(c *gin.Context) {
	variants, details, err := h.products.ListVariantsForOwner(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVariantResponseList(variants, details))
}

func paginationParams(c *gin.Context) (limit, offset int) {
	limit = parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset = parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)
	return limit, offset
}
