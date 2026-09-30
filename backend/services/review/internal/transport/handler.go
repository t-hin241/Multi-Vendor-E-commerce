package transport

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

const maxUploadBytes = 6 * 1024 * 1024

type Handler struct {
	reviews *usecase.ReviewUseCase
	log     zerolog.Logger
}

func NewHandler(reviews *usecase.ReviewUseCase, log zerolog.Logger) *Handler {
	return &Handler{reviews: reviews, log: log}
}
func page(c *gin.Context) (int, int) {
	limit := 20
	if n, e := strconv.Atoi(c.Query("limit")); e == nil && n >= 1 && n <= 100 {
		limit = n
	}
	offset := 0
	if n, e := strconv.Atoi(c.Query("offset")); e == nil && n >= 0 {
		offset = n
	}
	return limit, offset
}
func rating(c *gin.Context) int {
	n, _ := strconv.Atoi(c.Query("rating"))
	if n < 0 || n > 5 {
		return 0
	}
	return n
}

type reviewRequest struct {
	OrderItemID string `json:"order_item_id" binding:"required"`
	Rating      int    `json:"rating" binding:"required,min=1,max=5"`
	Comment     string `json:"comment" binding:"required"`
}
type replyRequest struct {
	VendorID string `json:"vendor_id" binding:"required"`
	Message  string `json:"message" binding:"required"`
}
type reportRequest struct {
	VendorID string  `json:"vendor_id" binding:"required"`
	ReasonID string  `json:"reason_id" binding:"required"`
	Note     *string `json:"note"`
}
type reasonRequest struct {
	Code        string  `json:"code" binding:"required"`
	Label       string  `json:"label" binding:"required"`
	Description *string `json:"description"`
	IsActive    *bool   `json:"is_active"`
}
type resolveRequest struct {
	Decision string  `json:"decision" binding:"required,oneof=keep hide"`
	ReasonID string  `json:"reason_id"`
	Note     *string `json:"note"`
}
type imageResponse struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Position int    `json:"position"`
}
type replyResponse struct {
	VendorID  string `json:"vendor_id"`
	Message   string `json:"message"`
	UpdatedAt string `json:"updated_at"`
}
type reviewResponse struct {
	ID               string          `json:"id"`
	BuyerID          string          `json:"buyer_id,omitempty"`
	BuyerName        string          `json:"buyer_name,omitempty"`
	ProductID        string          `json:"product_id"`
	VendorID         string          `json:"vendor_id,omitempty"`
	OrderItemID      string          `json:"order_item_id,omitempty"`
	Rating           int             `json:"rating"`
	Comment          string          `json:"comment"`
	Status           string          `json:"status,omitempty"`
	VerifiedPurchase bool            `json:"verified_purchase"`
	Images           []imageResponse `json:"images"`
	Reply            *replyResponse  `json:"reply,omitempty"`
	CreatedAt        string          `json:"created_at"`
}

func (h *Handler) response(c *gin.Context, v *domain.Review, internal, admin bool) (reviewResponse, error) {
	images, err := h.reviews.Images(c.Request.Context(), v.ID)
	if err != nil {
		return reviewResponse{}, err
	}
	reply, err := h.reviews.GetReply(c.Request.Context(), v.ID)
	if err != nil {
		return reviewResponse{}, err
	}
	out := reviewResponse{ID: v.ID, BuyerName: v.BuyerName, ProductID: v.ProductID, Rating: v.Rating, Comment: v.Comment, VerifiedPurchase: true, Images: make([]imageResponse, 0, len(images)), CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339)}
	if reply != nil {
		out.Reply = &replyResponse{VendorID: reply.VendorID, Message: reply.Message, UpdatedAt: reply.UpdatedAt.UTC().Format(time.RFC3339)}
	}
	if internal {
		out.VendorID = v.VendorID
		out.OrderItemID = v.OrderItemID
		out.Status = string(v.Status)
	}
	if admin {
		out.BuyerID = v.BuyerID
	}
	for _, image := range images {
		out.Images = append(out.Images, imageResponse{ID: image.ID, URL: image.URL, Position: image.Position})
	}
	return out, nil
}
func (h *Handler) responses(c *gin.Context, items []*domain.Review, internal, admin bool) ([]reviewResponse, error) {
	out := make([]reviewResponse, 0, len(items))
	for _, v := range items {
		response, err := h.response(c, v, internal, admin)
		if err != nil {
			return nil, err
		}
		out = append(out, response)
	}
	return out, nil
}
func summary(s domain.Summary) gin.H {
	return gin.H{"rating_average": s.RatingAverage, "rating_count": s.RatingCount, "rating_distribution": s.Distribution}
}
func (h *Handler) ListPublic(c *gin.Context) {
	limit, offset := page(c)
	items, s, err := h.reviews.ListPublic(c.Request.Context(), c.Param("productID"), rating(c), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	responses, err := h.responses(c, items, false, false)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"reviews": responses, "summary": summary(s)})
}
func (h *Handler) Summary(c *gin.Context) {
	_, s, err := h.reviews.ListPublic(c.Request.Context(), c.Param("productID"), 0, 1, 0)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, summary(s))
}
func (h *Handler) Eligibility(c *gin.Context) {
	items, err := h.reviews.ListEligibility(c.Request.Context(), middleware.GetUserID(c), c.Query("product_id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, items)
}
func (h *Handler) Create(c *gin.Context) {
	var req reviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", err.Error())
		return
	}
	v, err := h.reviews.Create(c.Request.Context(), middleware.GetUserID(c), req.OrderItemID, req.Rating, req.Comment)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	response, err := h.response(c, v, true, false)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 201, response)
}
func (h *Handler) ListMine(c *gin.Context) {
	limit, offset := page(c)
	items, err := h.reviews.ListMine(c.Request.Context(), middleware.GetUserID(c), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	responses, err := h.responses(c, items, true, false)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, responses)
}
func (h *Handler) UploadImage(c *gin.Context) {
	f, err := c.FormFile("image")
	if err != nil {
		httpresponse.Error(c, 400, "validation_error", "An image file is required")
		return
	}
	if f.Size > maxUploadBytes {
		httpresponse.Error(c, 400, "validation_error", "Image must be no larger than 5MB")
		return
	}
	file, err := f.Open()
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
	image, err := h.reviews.UploadImage(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), f.Header.Get("Content-Type"), data)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 201, imageResponse{ID: image.ID, URL: image.URL, Position: image.Position})
}
func (h *Handler) ListVendor(c *gin.Context) {
	limit, offset := page(c)
	var replied *bool
	if q := c.Query("replied"); q == "true" {
		b := true
		replied = &b
	} else if q == "false" {
		b := false
		replied = &b
	}
	items, err := h.reviews.ListVendor(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("product_id"), rating(c), replied, limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	responses, err := h.responses(c, items, true, false)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, responses)
}
func (h *Handler) VendorSummary(c *gin.Context) {
	s, err := h.reviews.VendorSummary(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, summary(s))
}
func (h *Handler) Reply(c *gin.Context) {
	var req replyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", err.Error())
		return
	}
	reply, err := h.reviews.Reply(c.Request.Context(), middleware.GetUserID(c), req.VendorID, c.Param("id"), req.Message)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, replyResponse{VendorID: reply.VendorID, Message: reply.Message, UpdatedAt: reply.UpdatedAt.UTC().Format(time.RFC3339)})
}
func (h *Handler) ActiveReasons(c *gin.Context) {
	x, err := h.reviews.ActiveReasons(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, x)
}
func (h *Handler) Report(c *gin.Context) {
	var req reportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", err.Error())
		return
	}
	x, err := h.reviews.Report(c.Request.Context(), middleware.GetUserID(c), req.VendorID, c.Param("id"), req.ReasonID, req.Note)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 201, x)
}
func (h *Handler) ListAdmin(c *gin.Context) {
	limit, offset := page(c)
	x, err := h.reviews.ListAdmin(c.Request.Context(), c.Query("buyer_id"), c.Query("vendor_id"), c.Query("product_id"), c.Query("status"), rating(c), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	responses, err := h.responses(c, x, true, true)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, responses)
}
func (h *Handler) ListReports(c *gin.Context) {
	limit, offset := page(c)
	x, err := h.reviews.ListReports(c.Request.Context(), c.Query("status"), c.Query("vendor_id"), c.Query("product_id"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, x)
}
func (h *Handler) ResolveReport(c *gin.Context) {
	var req resolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", err.Error())
		return
	}
	if err := h.reviews.ResolveReport(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), domain.ReportDecision(req.Decision), req.ReasonID, req.Note); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, gin.H{"resolved": true})
}
func (h *Handler) ListReasons(c *gin.Context) {
	x, err := h.reviews.ListReasons(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, x)
}
func (h *Handler) CreateReason(c *gin.Context) {
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", err.Error())
		return
	}
	x, err := h.reviews.CreateReason(c.Request.Context(), req.Code, req.Label, req.Description)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 201, x)
}
func (h *Handler) UpdateReason(c *gin.Context) {
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, 400, "validation_error", err.Error())
		return
	}
	current, err := h.reviews.ListReasons(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	active := true
	for _, reason := range current {
		if reason.ID == c.Param("id") {
			active = reason.IsActive
			break
		}
	}
	if req.IsActive != nil {
		active = *req.IsActive
	}
	x, err := h.reviews.UpdateReason(c.Request.Context(), c.Param("id"), req.Code, req.Label, req.Description, active)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, 200, x)
}
