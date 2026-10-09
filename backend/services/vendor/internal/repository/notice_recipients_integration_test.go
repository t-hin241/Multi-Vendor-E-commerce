package repository_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess/adminaccesstest"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/transport"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// AF-08: a shop notice goes to the owner and to staff holding the
// purpose's permission; revoked staff, staff while the feature is off and
// accounts that may not act are left out, and the version follows the list.
func TestIntegrationNoticeRecipientsFollowPermissions(t *testing.T) {
	f := setup(t)
	v := f.shop(t)
	ctx := t.Context()
	uc := staffUseCase(f, &capturedMail{})
	staff := repository.StaffRepository{Pool: f.db}
	clerk, accountant := uuid.NewString(), uuid.NewString()
	f.ops.Actors.(actors)[clerk] = "buyer"
	f.ops.Actors.(actors)[accountant] = "buyer"
	if _, err := staff.JoinAsStaff(ctx, v.ID, clerk, uuid.NewString(), []string{shopaccess.OrdersFulfill, shopaccess.ReturnsHandle}); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.JoinAsStaff(ctx, v.ID, accountant, uuid.NewString(), []string{shopaccess.FinanceRead}); err != nil {
		t.Fatal(err)
	}
	users := func(purpose string) ([]string, string) {
		t.Helper()
		res, err := uc.NotificationRecipients(ctx, v.ID, purpose)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range res.Recipients {
			out = append(out, r.Role+":"+r.UserID)
		}
		return out, res.PermissionVersion
	}
	has := func(list []string, want ...string) bool {
		if len(list) != len(want) {
			return false
		}
		for _, w := range want {
			found := false
			for _, l := range list {
				found = found || l == w
			}
			if !found {
				return false
			}
		}
		return true
	}
	orders, ordersVersion := users("orders")
	if !has(orders, "owner:"+f.owner, "staff:"+clerk) {
		t.Fatalf("orders recipients %v", orders)
	}
	if finance, _ := users("finance"); !has(finance, "owner:"+f.owner, "staff:"+accountant) {
		t.Fatalf("finance recipients %v", finance)
	}
	if again, v2 := users("orders"); !has(again, orders...) || v2 != ordersVersion {
		t.Fatal("the same list must keep its version")
	}

	uc.Enabled = false
	if off, _ := users("orders"); !has(off, "owner:"+f.owner) {
		t.Fatalf("staff must not receive while shop staff is off: %v", off)
	}
	uc.Enabled = true

	m, err := uc.GetMember(ctx, f.owner, v.ID, clerk)
	if err != nil {
		t.Fatal(err)
	}
	if err := uc.RemoveMember(ctx, f.owner, v.ID, clerk, m.Version, nil); err != nil {
		t.Fatal(err)
	}
	after, afterVersion := users("orders")
	if !has(after, "owner:"+f.owner) || afterVersion == ordersVersion {
		t.Fatalf("revoked staff still listed or version unchanged: %v", after)
	}

	delete(f.ops.Actors.(actors), f.owner) // the owner's account is locked
	if locked, _ := users("orders"); len(locked) != 0 {
		t.Fatalf("a locked owner must not receive notices: %v", locked)
	}

	if _, err := uc.NotificationRecipients(ctx, v.ID, "marketing"); err == nil {
		t.Fatal("unknown purpose accepted")
	}
	if _, err := uc.NotificationRecipients(ctx, uuid.NewString(), "orders"); err == nil {
		t.Fatal("unknown shop answered")
	}

	gin.SetMode(gin.ReleaseMode)
	log := zerolog.Nop()
	router := transport.NewRouter("test", log, authjwttest.Manager(), transport.NewVendorHandler(f.uc, log), transport.NewVendorAddressHandler(f.addresses, log),
		transport.NewAdminHandler(f.uc, log), transport.NewInternalHandler(f.uc, log), transport.NewPolicyHandler(&usecase.PolicyUseCase{}, f.uc, log),
		transport.NewStaffHandler(uc, log), adminaccesstest.Guard(transport.AdminRoutes), serviceauth.SharedKey("test-internal-service-key"))
	f.ops.Actors.(actors)[f.owner] = "vendor"
	get := func(key, path string) (int, map[string]any) {
		req := httptest.NewRequest("GET", path, nil)
		if key != "" {
			req.Header.Set(serviceauth.Header, key)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var out struct{ Data map[string]any }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out.Data
	}
	path := "/internal/vendors/" + v.ID + "/notification-recipients?purpose=finance"
	if code, _ := get("", path); code != 403 {
		t.Fatalf("recipients without the service key: %d", code)
	}
	code, body := get("test-internal-service-key", path)
	if code != 200 || len(body["recipients"].([]any)) != 2 || body["permission_version"] == "" {
		t.Fatalf("recipients answered %d %v", code, body)
	}
	if code, _ := get("test-internal-service-key", "/internal/vendors/"+uuid.NewString()+"/notification-recipients?purpose=orders"); code != 404 {
		t.Fatalf("unknown shop answered %d", code)
	}
}
