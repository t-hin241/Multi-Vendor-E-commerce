package transport

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/cart/internal/usecase"
)

// bindJSON decodes and validates the body, answering 400 with the failing
// field names (never the raw decoder error) when it is invalid.
func bindJSON(c *gin.Context, req any) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", describeBindError(err))
		return false
	}
	return true
}

func describeBindError(err error) string {
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		parts := make([]string, 0, len(verrs))
		for _, fe := range verrs {
			parts = append(parts, fieldPath(fe)+" "+ruleMessage(fe))
		}
		return "Invalid request: " + strings.Join(parts, "; ")
	}
	return "Invalid request body"
}

func fieldPath(fe validator.FieldError) string {
	// Namespace is e.g. "confirmPricesRequest.Lines[0].LineID"; drop the
	// struct name and use snake_case field names clients know.
	ns := fe.Namespace()
	if i := strings.Index(ns, "."); i >= 0 {
		ns = ns[i+1:]
	}
	return toSnake(ns)
}

func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && s[i-1] != '.' && s[i-1] != '[' {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	// "line_i_d" → "line_id", "product_i_d" → "product_id"
	return strings.ReplaceAll(b.String(), "_i_d", "_id")
}

func ruleMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "uuid":
		return "must be a valid id"
	case "min":
		return "must be at least " + fe.Param()
	case "max":
		return "must be at most " + fe.Param()
	case "len":
		return "must have length " + fe.Param()
	default:
		return "is invalid"
	}
}

func validUUID(v string) bool {
	_, err := uuid.Parse(v)
	return err == nil
}

// optionalVariantID reads the ?variant_id= query param used to target a
// variant-scoped line on the shared :productID route — omitted, it targets
// the product-level (no-variant) line.
func optionalVariantID(c *gin.Context) (*string, bool) {
	v := c.Query("variant_id")
	if v == "" {
		return nil, true
	}
	if !validUUID(v) {
		return nil, false
	}
	return &v, true
}

// expectedVersionParam reads the optional cart version a DELETE is
// conditional on, from ?expected_version= or an If-Match header.
func expectedVersionParam(c *gin.Context) (*int64, bool) {
	raw := c.Query("expected_version")
	if raw == "" {
		raw = strings.Trim(c.GetHeader("If-Match"), `" `)
	}
	if raw == "" {
		return nil, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 1 {
		return nil, false
	}
	return &v, true
}

// pageParams reads ?limit=&offset= for the cart view.
func pageParams(c *gin.Context) (usecase.Page, bool) {
	page := usecase.Page{Limit: usecase.DefaultPageLimit}
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > usecase.MaxPageLimit {
			return page, false
		}
		page.Limit = v
	}
	if raw := c.Query("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 || v > usecase.MaxPageOffset {
			return page, false
		}
		page.Offset = v
	}
	return page, true
}
