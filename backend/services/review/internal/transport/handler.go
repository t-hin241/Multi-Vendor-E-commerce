package transport

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/review/internal/adapter"
	"shopee/backend/services/review/internal/domain"
	"shopee/backend/services/review/internal/repository"
	"shopee/backend/services/review/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

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
	if n, e := strconv.Atoi(c.Query("offset")); e == nil && n >= 0 && n <= 10_000 {
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

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

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
type hideRequest struct {
	ReasonID string  `json:"reason_id" binding:"required"`
	Note     *string `json:"note"`
}
type restoreRequest struct {
	Note *string `json:"note"`
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

// reviewResponse is one review. The public view carries only the masked
// author label, never the buyer's id, email, address or full name.
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
	HiddenReasonID   *string         `json:"hidden_reason_id,omitempty"`
	HiddenNote       *string         `json:"hidden_note,omitempty"`
	HiddenAt         *string         `json:"hidden_at,omitempty"`
	CreatedAt        string          `json:"created_at"`
}

type view int

const (
	publicView view = iota // storefront
	ownerView              // the buyer's own reviews, the shop's reviews
	adminView
)

func reviews(p *usecase.Page, v view) []reviewResponse {
	out := make([]reviewResponse, 0, len(p.Reviews))
	for _, r := range p.Reviews {
		out = append(out, review(r, p.Images[r.ID], p.Replies[r.ID], v))
	}
	return out
}

func review(r *domain.Review, images []*domain.Image, reply *domain.Reply, v view) reviewResponse {
	out := reviewResponse{ID: r.ID, ProductID: r.ProductID, Rating: r.Rating, Comment: r.Comment, VerifiedPurchase: r.VerifiedPurchase,
		Images: make([]imageResponse, 0, len(images)), CreatedAt: timestamp(r.CreatedAt)}
	if r.AuthorLabel != nil {
		out.BuyerName = *r.AuthorLabel
	}
	for _, image := range images {
		out.Images = append(out.Images, imageResponse{ID: image.ID, URL: image.URL, Position: image.Position})
	}
	if reply != nil {
		out.Reply = &replyResponse{VendorID: reply.VendorID, Message: reply.Message, UpdatedAt: timestamp(reply.UpdatedAt)}
	}
	if v >= ownerView {
		out.VendorID, out.OrderItemID, out.Status = r.VendorID, r.OrderItemID, string(r.Status)
	}
	if v == adminView {
		out.BuyerID, out.HiddenReasonID, out.HiddenNote = r.BuyerID, r.HiddenReasonID, r.HiddenNote
		if r.HiddenAt != nil {
			at := timestamp(*r.HiddenAt)
			out.HiddenAt = &at
		}
	}
	return out
}

func summary(s domain.Summary) gin.H {
	return gin.H{"rating_average": s.RatingAverage, "rating_count": s.RatingCount, "rating_distribution": s.Distribution}
}

type reasonResponse struct {
	ID          string  `json:"id"`
	Code        string  `json:"code"`
	Label       string  `json:"label"`
	Description *string `json:"description,omitempty"`
	IsActive    bool    `json:"is_active"`
}

func reasons(items []*domain.Reason) []reasonResponse {
	out := make([]reasonResponse, 0, len(items))
	for _, x := range items {
		out = append(out, reasonResponse{ID: x.ID, Code: x.Code, Label: x.Label, Description: x.Description, IsActive: x.IsActive})
	}
	return out
}

type reportResponse struct {
	ID                 string  `json:"id"`
	ReviewID           string  `json:"review_id"`
	ReportingVendorID  string  `json:"reporting_vendor_id"`
	ReasonID           string  `json:"reason_id"`
	ReasonCode         string  `json:"reason_code"`
	ReasonLabel        string  `json:"reason_label"`
	Note               *string `json:"note,omitempty"`
	Status             string  `json:"status"`
	Decision           *string `json:"decision,omitempty"`
	ResolutionReasonID *string `json:"resolution_reason_id,omitempty"`
	ResolutionNote     *string `json:"resolution_note,omitempty"`
	ResolvedAt         *string `json:"resolved_at,omitempty"`
	CreatedAt          string  `json:"created_at"`
}

func report(x *domain.Report) reportResponse {
	out := reportResponse{ID: x.ID, ReviewID: x.ReviewID, ReportingVendorID: x.ReportingVendorID, ReasonID: x.ReasonID, ReasonCode: x.ReasonCode,
		ReasonLabel: x.ReasonLabel, Note: x.Note, Status: string(x.Status), ResolutionReasonID: x.ResolutionReasonID,
		ResolutionNote: x.ResolutionNote, CreatedAt: timestamp(x.CreatedAt)}
	if x.Decision != nil {
		d := string(*x.Decision)
		out.Decision = &d
	}
	if x.ResolvedAt != nil {
		at := timestamp(*x.ResolvedAt)
		out.ResolvedAt = &at
	}
	return out
}

func (h *Handler) fail(c *gin.Context, err error) { httpresponse.HandleError(c, h.log, err) }

func (h *Handler) bind(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid request body")
		return false
	}
	return true
}

func (h *Handler) ListPublic(c *gin.Context) {
	limit, offset := page(c)
	p, s, err := h.reviews.ListPublic(c.Request.Context(), c.Param("productID"), rating(c), limit, offset)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"reviews": reviews(p, publicView), "summary": summary(s)})
}

func (h *Handler) Summary(c *gin.Context) {
	s, err := h.reviews.Summary(c.Request.Context(), c.Param("productID"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, summary(s))
}

func (h *Handler) Eligibility(c *gin.Context) {
	items, err := h.reviews.Eligibility(c.Request.Context(), middleware.GetUserID(c), c.Query("product_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	if items == nil {
		items = []adapter.EligibleOrderItem{}
	}
	httpresponse.OK(c, http.StatusOK, items)
}

func (h *Handler) Create(c *gin.Context) {
	var req reviewRequest
	if !h.bind(c, &req) {
		return
	}
	v, err := h.reviews.Create(c.Request.Context(), middleware.GetUserID(c), req.OrderItemID, req.Rating, req.Comment)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, review(v, nil, nil, ownerView))
}

func (h *Handler) ListMine(c *gin.Context) {
	limit, offset := page(c)
	p, err := h.reviews.ListMine(c.Request.Context(), middleware.GetUserID(c), limit, offset)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, reviews(p, ownerView))
}

// maxUploadBody bounds the whole multipart request (image plus framing).
const maxUploadBody = domain.MaxImageBytes + 64*1024

func (h *Handler) UploadImage(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBody)
	f, err := c.FormFile("image")
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "An image file of at most 5MB is required")
		return
	}
	if f.Size > domain.MaxImageBytes {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Image must be no larger than 5MB")
		return
	}
	file, err := f.Open()
	if err != nil {
		h.fail(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxImageBytes+1))
	if err != nil {
		h.fail(c, err)
		return
	}
	image, err := h.reviews.UploadImage(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), f.Header.Get("Content-Type"), data)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, imageResponse{ID: image.ID, URL: image.URL, Position: image.Position})
}

func (h *Handler) ListVendor(c *gin.Context) {
	limit, offset := page(c)
	var replied *bool
	if q := c.Query("replied"); q == "true" || q == "false" {
		b := q == "true"
		replied = &b
	}
	p, err := h.reviews.ListVendor(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("product_id"), rating(c), replied, limit, offset)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, reviews(p, ownerView))
}

func (h *Handler) VendorSummary(c *gin.Context) {
	s, err := h.reviews.VendorSummary(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, summary(s))
}

func (h *Handler) Reply(c *gin.Context) {
	var req replyRequest
	if !h.bind(c, &req) {
		return
	}
	reply, err := h.reviews.Reply(c.Request.Context(), middleware.GetUserID(c), req.VendorID, c.Param("id"), req.Message)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, replyResponse{VendorID: reply.VendorID, Message: reply.Message, UpdatedAt: timestamp(reply.UpdatedAt)})
}

func (h *Handler) ActiveReasons(c *gin.Context) {
	x, err := h.reviews.ActiveReasons(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, reasons(x))
}

func (h *Handler) Report(c *gin.Context) {
	var req reportRequest
	if !h.bind(c, &req) {
		return
	}
	x, err := h.reviews.Report(c.Request.Context(), middleware.GetUserID(c), req.VendorID, c.Param("id"), req.ReasonID, req.Note)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, report(x))
}

func (h *Handler) ListAdmin(c *gin.Context) {
	limit, offset := page(c)
	p, err := h.reviews.ListAdmin(c.Request.Context(), repository.AdminFilter{BuyerID: c.Query("buyer_id"), VendorID: c.Query("vendor_id"),
		ProductID: c.Query("product_id"), Status: c.Query("status"), Rating: rating(c)}, limit, offset)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, reviews(p, adminView))
}

func (h *Handler) ListReports(c *gin.Context) {
	limit, offset := page(c)
	x, err := h.reviews.ListReports(c.Request.Context(), c.Query("status"), c.Query("vendor_id"), c.Query("product_id"), limit, offset)
	if err != nil {
		h.fail(c, err)
		return
	}
	out := make([]reportResponse, 0, len(x))
	for _, r := range x {
		out = append(out, report(r))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *Handler) ResolveReport(c *gin.Context) {
	var req resolveRequest
	if !h.bind(c, &req) {
		return
	}
	if err := h.reviews.ResolveReport(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), domain.ReportDecision(req.Decision), req.ReasonID, req.Note); err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"resolved": true})
}

func (h *Handler) Hide(c *gin.Context) {
	var req hideRequest
	if !h.bind(c, &req) {
		return
	}
	if err := h.reviews.Hide(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.ReasonID, req.Note); err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"status": domain.ReviewHidden})
}

func (h *Handler) Restore(c *gin.Context) {
	var req restoreRequest
	if !h.bind(c, &req) {
		return
	}
	if err := h.reviews.Restore(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Note); err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"status": domain.ReviewPublished})
}

func (h *Handler) Operations(c *gin.Context) {
	counts, err := h.reviews.Operations(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"counts": counts, "generated_at": time.Now().UTC().Format(time.RFC3339)})
}

func (h *Handler) ListReasons(c *gin.Context) {
	x, err := h.reviews.ListReasons(c.Request.Context())
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, reasons(x))
}

func (h *Handler) CreateReason(c *gin.Context) {
	var req reasonRequest
	if !h.bind(c, &req) {
		return
	}
	x, err := h.reviews.CreateReason(c.Request.Context(), middleware.GetUserID(c), req.Code, req.Label, req.Description)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, reasons([]*domain.Reason{x})[0])
}

func (h *Handler) UpdateReason(c *gin.Context) {
	var req reasonRequest
	if !h.bind(c, &req) {
		return
	}
	x, err := h.reviews.UpdateReason(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Code, req.Label, req.Description, req.IsActive)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, reasons([]*domain.Reason{x})[0])
}
