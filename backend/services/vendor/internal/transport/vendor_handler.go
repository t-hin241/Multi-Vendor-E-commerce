package transport

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

const maxUploadBytes = 6 * 1024 * 1024 // slightly above the 5MB domain limit so the error is a clean validation message, not a truncated read

type VendorHandler struct {
	vendors *usecase.VendorUseCase
	log     zerolog.Logger
}

func NewVendorHandler(vendors *usecase.VendorUseCase, log zerolog.Logger) *VendorHandler {
	return &VendorHandler{vendors: vendors, log: log}
}

func (h *VendorHandler) Apply(c *gin.Context) {
	var req applyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	v, err := h.vendors.Apply(c.Request.Context(), middleware.GetUserID(c), req.ShopName, req.Description)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toVendorResponse(v))
}

// Mine lists every shop (any status) the caller owns — a user may own
// several under the 1:N vendor↔user relationship. Backs the frontend's
// shop switcher and "My Shops" management page.
func (h *VendorHandler) Mine(c *gin.Context) {
	vendors, err := h.vendors.ListByUserID(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponseList(vendors))
}

func (h *VendorHandler) Get(c *gin.Context) {
	v, err := h.vendors.GetOwned(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}

func (h *VendorHandler) UpdateProfile(c *gin.Context) {
	var req updateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	v, err := h.vendors.UpdateProfile(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), req.ShopName, req.Description, req.PolicyText)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}

func (h *VendorHandler) UploadLogo(c *gin.Context) {
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

	v, err := h.vendors.UploadLogo(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), fileHeader.Header.Get("Content-Type"), data)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}

func (h *VendorHandler) UploadBanner(c *gin.Context) {
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

	v, err := h.vendors.UploadBanner(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), fileHeader.Header.Get("Content-Type"), data)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toVendorResponse(v))
}

// GetPublic is the public shop page's read model — no auth, only an
// approved shop's branding is ever visible here.
func (h *VendorHandler) GetPublic(c *gin.Context) {
	v, err := h.vendors.GetPublicProfile(c.Request.Context(), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toPublicVendorResponse(v))
}
