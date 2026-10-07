package transport

import (
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
)

// AF-19: every admin route names the permission bundle it needs.
func TestEveryAdminRouteNamesAPermission(t *testing.T) {
	jwt := authjwttest.Manager()
	guard := adminaccesstest.Guard(AdminRoutes)
	verifier := serviceauth.SharedKey("fake-test-internal-key-not-a-real-secret")
	r := NewRouter("test", zerolog.Nop(), jwt, &VendorHandler{}, &VendorAddressHandler{}, &AdminHandler{}, &InternalHandler{},
		&PolicyHandler{}, &StaffHandler{}, guard, verifier)
	PayoutHandler{AdminGuard: guard}.Register(r, middleware.RequireAuth(jwt), "fake-test-payout-scope-key", verifier)
	adminaccesstest.AssertCovered(t, r, AdminRoutes)
}
