package transport

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/vendorreport"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

func DashboardHandler(d usecase.Dashboard, log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		r, err := vendorreport.Parse(c.Query("from"), c.Query("to"), c.Query("currency"))
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		out, err := d.Get(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), r)
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		httpresponse.OK(c, 200, out)
	}
}
