package bulkimport

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

// Handler is the HTTP surface of a Service.
type Handler struct {
	S   *Service
	Log zerolog.Logger
}

// Register mounts the vendor routes on /api/<owner>/vendor and the
// operator routes on /api/<owner>/admin. Authentication, the seller
// console guard and the admin bundle guard come from the groups; every use
// case still checks the shop permission itself.
func Register(vendor, admin *gin.RouterGroup, h Handler) {
	noStore := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
	vendor.GET("/import-templates/:kind", noStore, h.template)
	vendor.GET("/import-jobs", noStore, h.jobs)
	vendor.POST("/import-jobs", h.upload)
	vendor.GET("/import-jobs/:id", noStore, h.job)
	vendor.GET("/import-jobs/:id/rows", noStore, h.rows)
	vendor.POST("/import-jobs/:id/confirm", h.confirm)
	vendor.POST("/import-jobs/:id/cancellations", h.cancel)
	vendor.POST("/import-jobs/:id/report-downloads", noStore, h.report)
	vendor.POST("/export-jobs", h.createExport)
	vendor.GET("/export-jobs/:id", noStore, h.export)
	vendor.POST("/export-jobs/:id/download-links", noStore, h.exportDownload)
	if admin != nil {
		admin.GET("/import-jobs", noStore, h.adminJobs)
		admin.POST("/import-jobs/:id/cancellations", h.adminCancel)
	}
}

func (h Handler) fail(c *gin.Context, err error) {
	httpresponse.HandleError(c, h.Log, err)
}

func limit(c *gin.Context) (int, error) {
	n, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil {
		return 0, apperror.Validation("limit must be 1-100")
	}
	return n, nil
}

func (h Handler) template(c *gin.Context) {
	t, err := h.S.Template(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Param("kind"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, t)
}

func (h Handler) jobs(c *gin.Context) {
	n, err := limit(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.S.Jobs(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("kind"), c.Query("cursor"), n)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, p)
}

func (h Handler) upload(c *gin.Context) {
	// Room for the multipart framing around a file at the byte limit.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.S.MaxBytes+64<<10)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.fail(c, FileInvalid("File is too large"))
			return
		}
		h.fail(c, apperror.Validation("file is required (multipart field \"file\")"))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, h.S.MaxBytes+1))
	if err != nil {
		h.fail(c, apperror.Validation("Could not read the file"))
		return
	}
	if int64(len(data)) > h.S.MaxBytes {
		h.fail(c, FileInvalid("File is too large"))
		return
	}
	version, err := strconv.Atoi(c.Request.FormValue("template_version"))
	if err != nil {
		h.fail(c, apperror.Validation("template_version is required"))
		return
	}
	j, err := h.S.Upload(c.Request.Context(), middleware.GetUserID(c), c.GetHeader("Idempotency-Key"), UploadInput{
		VendorID: c.Request.FormValue("vendor_id"), Kind: c.Request.FormValue("kind"), TemplateVersion: version, FileName: header.Filename, Data: data})
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, j)
}

func (h Handler) job(c *gin.Context) {
	j, err := h.S.Job(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, j)
}

func (h Handler) rows(c *gin.Context) {
	n, err := limit(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.S.Rows(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), c.Query("status"), c.Query("cursor"), n)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, p)
}

func (h Handler) confirm(c *gin.Context) {
	var in ConfirmInput
	if err := c.ShouldBindJSON(&in); err != nil || in.PreviewHash == "" || in.ExpectedVersion < 1 {
		h.fail(c, apperror.Validation("preview_hash and expected_version are required"))
		return
	}
	j, err := h.S.Confirm(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), in)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, j)
}

type reasonBody struct {
	Reason string `json:"reason"`
}

func (h Handler) cancel(c *gin.Context) {
	var in reasonBody
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&in); err != nil {
			h.fail(c, apperror.Validation("Invalid cancellation request"))
			return
		}
	}
	j, err := h.S.Cancel(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), in.Reason)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, j)
}

func (h Handler) report(c *gin.Context) {
	d, err := h.S.ReportDownload(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, d)
}

func (h Handler) createExport(c *gin.Context) {
	var in struct {
		VendorID string `json:"vendor_id"`
		Kind     string `json:"kind"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		h.fail(c, apperror.Validation("vendor_id and kind are required"))
		return
	}
	e, err := h.S.CreateExport(c.Request.Context(), middleware.GetUserID(c), in.VendorID, in.Kind, c.GetHeader("Idempotency-Key"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, e)
}

func (h Handler) export(c *gin.Context) {
	e, err := h.S.Export(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, e)
}

func (h Handler) exportDownload(c *gin.Context) {
	d, err := h.S.ExportDownload(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, d)
}

func (h Handler) adminJobs(c *gin.Context) {
	n, err := limit(c)
	if err != nil {
		h.fail(c, err)
		return
	}
	p, err := h.S.AdminJobs(c.Request.Context(), c.Query("vendor_id"), c.Query("status"), c.Query("cursor"), n)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, p)
}

func (h Handler) adminCancel(c *gin.Context) {
	var in reasonBody
	if err := c.ShouldBindJSON(&in); err != nil {
		h.fail(c, apperror.Validation("reason is required"))
		return
	}
	j, err := h.S.AdminCancel(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), in.Reason)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, j)
}
