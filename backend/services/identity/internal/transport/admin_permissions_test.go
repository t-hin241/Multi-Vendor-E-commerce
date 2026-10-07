package transport

import (
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/services/identity/internal/usecase"
)

// AF-19: every admin route names the permission bundle it needs.
func TestEveryAdminRouteNamesAPermission(t *testing.T) {
	r := NewRouter("test", zerolog.Nop(), authjwttest.Manager(), &AuthHandler{}, &AdminHandler{}, &InternalHandler{}, &AccessHandler{},
		adminaccesstest.Guard(AdminRoutes), Security{}, &usecase.ResetDeliveryUseCase{})
	adminaccesstest.AssertCovered(t, r, AdminRoutes)
}
