package transport

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/usecase"
)

// EvidenceHandler serves PW-038 failure report evidence: the shop and the
// admins upload a file for a shipment, cite it in their report, and read
// the evidence of its reports back through Shipment.
type EvidenceHandler struct {
	Shipments *usecase.ShipmentUseCase
	Log       zerolog.Logger
}

type evidenceResponse struct {
	ID          string    `json:"id"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	State       string    `json:"state"`
	ReportKind  *string   `json:"report_kind,omitempty"`
	OwnerRole   string    `json:"owner_role"`
	CreatedAt   time.Time `json:"created_at"`
}

func toEvidence(e *domain.Evidence) evidenceResponse {
	return evidenceResponse{ID: e.ID, ContentType: e.ContentType, SizeBytes: e.SizeBytes, State: e.State,
		ReportKind: e.ReportKind, OwnerRole: string(e.OwnerRole), CreatedAt: e.CreatedAt}
}

// Register adds the routes for shop members (seller console) and to an
// admin group already guarded by AdminRoutes.
func (h EvidenceHandler) Register(r *gin.Engine, jwtManager *authjwt.Manager, adminGroup *gin.RouterGroup) {
	seller := r.Group("/api/shipments", middleware.RequireAuth(jwtManager), middleware.SellerConsole())
	h.routes(seller, "/:id/evidence", vendor)
	h.routes(adminGroup, "/shipments/:id/evidence", admin)
}

func (h EvidenceHandler) routes(g *gin.RouterGroup, path string, actor func(*gin.Context) usecase.Actor) {
	g.POST(path, func(c *gin.Context) { h.upload(c, actor(c)) })
	g.GET(path, func(c *gin.Context) {
		if !validShipmentID(c) {
			return
		}
		items, err := h.Shipments.ListEvidence(c.Request.Context(), actor(c), c.Param("id"))
		if err != nil {
			httpresponse.HandleError(c, h.Log, err)
			return
		}
		out := make([]evidenceResponse, 0, len(items))
		for _, e := range items {
			out = append(out, toEvidence(e))
		}
		httpresponse.OK(c, http.StatusOK, out)
	})
	g.GET(path+"/:evidenceId", func(c *gin.Context) {
		if !validShipmentID(c) {
			return
		}
		if _, err := uuid.Parse(c.Param("evidenceId")); err != nil {
			httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid evidence id")
			return
		}
		file, err := h.Shipments.OpenEvidence(c.Request.Context(), actor(c), c.Param("id"), c.Param("evidenceId"))
		if err != nil {
			httpresponse.HandleError(c, h.Log, err)
			return
		}
		defer file.Body.Close()
		c.DataFromReader(http.StatusOK, file.SizeBytes, file.ContentType, file.Body, map[string]string{
			"Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff", "Content-Disposition": "inline",
			"Content-Security-Policy": "default-src 'none'; sandbox",
		})
	})
}

// upload stores one photo or video (multipart field "file").
func (h EvidenceHandler) upload(c *gin.Context, actor usecase.Actor) {
	if !validShipmentID(c) {
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresponse.HandleError(c, h.Log, domain.ErrEvidenceTooLarge)
			return
		}
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A file field named file is required")
		return
	}
	f, err := file.Open()
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "The file could not be read")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, domain.MaxEvidenceVideoBytes+1))
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "The file could not be read")
		return
	}
	e, err := h.Shipments.UploadEvidence(c.Request.Context(), actor, c.Param("id"), data)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toEvidence(e))
}
