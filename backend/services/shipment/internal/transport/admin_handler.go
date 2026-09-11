package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/shipment/internal/usecase"
)

// AdminHandler serves the admin-only carrier/zone/fee-rule management
// routes — plain admin-authored reference data and rules, guarded purely
// by middleware.RequireRole("admin") in this service's own router, the
// same convention Vendor and Catalog already use for their own admin
// groups rather than a centralized Admin service.
type AdminHandler struct {
	carriers *usecase.CarrierUseCase
	zones    *usecase.ZoneUseCase
	feeRules *usecase.FeeRuleUseCase
	log      zerolog.Logger
}

func NewAdminHandler(carriers *usecase.CarrierUseCase, zones *usecase.ZoneUseCase, feeRules *usecase.FeeRuleUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{carriers: carriers, zones: zones, feeRules: feeRules, log: log}
}

func (h *AdminHandler) CreateCarrier(c *gin.Context) {
	var req createCarrierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	carrier, err := h.carriers.Create(c.Request.Context(), req.Name, req.Code)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toCarrierResponse(carrier))
}

func (h *AdminHandler) ListCarriers(c *gin.Context) {
	carriers, err := h.carriers.List(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toCarrierResponseList(carriers))
}

// ListActiveCarriers is the public/vendor-facing read of carriers, so a
// vendor can discover which ones exist to enable for their own shop — the
// admin-only ListCarriers above also returns inactive ones, meant for
// admin's own management view.
func (h *AdminHandler) ListActiveCarriers(c *gin.Context) {
	carriers, err := h.carriers.ListActive(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toCarrierResponseList(carriers))
}

func (h *AdminHandler) SetCarrierActive(c *gin.Context) {
	var req setCarrierActiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.carriers.SetActive(c.Request.Context(), c.Param("id"), req.IsActive); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"updated": true})
}

func (h *AdminHandler) CreateZone(c *gin.Context) {
	var req createZoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	zone, err := h.zones.Create(c.Request.Context(), req.Name, req.Code)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toZoneResponse(zone))
}

func (h *AdminHandler) ListZones(c *gin.Context) {
	zones, err := h.zones.List(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toZoneResponseList(zones))
}

func (h *AdminHandler) AddProvinceToZone(c *gin.Context) {
	var req addProvinceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.zones.AddProvince(c.Request.Context(), c.Param("id"), req.ProvinceCode); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, gin.H{"added": true})
}

func (h *AdminHandler) ListZoneProvinces(c *gin.Context) {
	provinces, err := h.zones.ListProvinces(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, provinces)
}

func (h *AdminHandler) SetFeeRule(c *gin.Context) {
	var req setFeeRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	actorUserID := middleware.GetUserID(c)
	rule, err := h.feeRules.SetCurrent(c.Request.Context(), req.CarrierID, req.ZoneID, req.BaseFeeAmount, req.FreeWeightGrams, req.ExtraFeePerKg, actorUserID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toFeeRuleResponse(rule))
}

func (h *AdminHandler) ListFeeRules(c *gin.Context) {
	rules, err := h.feeRules.List(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toFeeRuleResponseList(rules))
}
