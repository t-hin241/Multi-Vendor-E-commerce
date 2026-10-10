package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// PW-038: the evidence images of a return's receipt or lost parcel and of
// a failed delivery's receipt, for the shop and admins.

type evidenceResponse struct {
	ID          string    `json:"id"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

func toEvidence(items []*domain.CaseAttachment) []evidenceResponse {
	out := make([]evidenceResponse, 0, len(items))
	for _, a := range items {
		out = append(out, evidenceResponse{ID: a.ID, ContentType: a.ContentType, SizeBytes: a.SizeBytes, CreatedAt: a.CreatedAt})
	}
	return out
}

// evidenceRoutes serves list and download of one kind of reference whose
// id is the route parameter param.
func (h *SupportHandler) evidenceRoutes(g gin.IRoutes, prefix, param, receiptType string) {
	g.GET(prefix+"/:"+param+"/evidence", func(c *gin.Context) {
		if !validID(c, c.Param(param)) {
			return
		}
		items, err := h.orders.ListReceiptEvidence(c.Request.Context(), actorOf(c), receiptType, c.Param(param))
		if err != nil {
			httpresponse.HandleError(c, h.log, err)
			return
		}
		httpresponse.OK(c, http.StatusOK, toEvidence(items))
	})
	g.GET(prefix+"/:"+param+"/evidence/:attachmentID", func(c *gin.Context) {
		if !validID(c, c.Param(param)) || !validID(c, c.Param("attachmentID")) {
			return
		}
		file, err := h.orders.OpenReceiptEvidence(c.Request.Context(), actorOf(c), receiptType, c.Param(param), c.Param("attachmentID"))
		if err != nil {
			httpresponse.HandleError(c, h.log, err)
			return
		}
		defer file.Body.Close()
		c.DataFromReader(http.StatusOK, file.SizeBytes, file.ContentType, file.Body, map[string]string{
			"Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff", "Content-Disposition": "inline",
			"Content-Security-Policy": "default-src 'none'; sandbox",
		})
	})
}

// RegisterReceiptEvidence adds the evidence routes to the shop's and the
// admins' groups.
func (h *SupportHandler) RegisterReceiptEvidence(vendor, admin gin.IRoutes) {
	for _, g := range []gin.IRoutes{vendor, admin} {
		h.evidenceRoutes(g, "/return-requests", "id", usecase.EvidenceReturn)
		h.evidenceRoutes(g, "/delivery-exceptions", "exceptionID", usecase.EvidenceDeliveryException)
	}
}
