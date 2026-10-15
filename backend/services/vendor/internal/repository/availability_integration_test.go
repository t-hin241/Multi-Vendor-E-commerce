package repository_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/authjwt/authjwttest"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/pkg/vendorsales"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/transport"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

func vacationUseCase(f *fixture, now time.Time) *usecase.AvailabilityUseCase {
	return &usecase.AvailabilityUseCase{Store: repository.AvailabilityRepository{Pool: f.db}, Vendors: f.vendors,
		Access: staffUseCase(f, &capturedMail{}), Tx: f.ops.Tx, Events: f.ops.Events, Notices: f.ops.Notices,
		Enabled: true, Now: func() time.Time { return now }, Log: zerolog.Nop()}
}

func approvedVacationShop(t *testing.T, f *fixture) *domain.Vendor {
	t.Helper()
	v := f.shop(t)
	v, err := f.uc.Approve(t.Context(), v.ID, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVacationImmediateAtomicityAndConcurrentSchedule(t *testing.T) {
	f := setup(t)
	v := approvedVacationShop(t, f)
	now := time.Now().UTC().Truncate(time.Microsecond)
	uc := vacationUseCase(f, now)
	uc.Events = failEvents{}
	if _, err := uc.Schedule(t.Context(), f.owner, v.ID, domain.VacationInput{}); err == nil {
		t.Fatal("expected outbox failure")
	}
	a, err := uc.Get(t.Context(), f.owner, v.ID)
	if err != nil || a.Current != nil || !a.AcceptingOrders || a.AvailabilityVersion != 0 || a.SaleVersion != v.Version {
		t.Fatalf("rollback: %+v %v", a, err)
	}
	uc.Events = f.ops.Events
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := uc.Schedule(t.Context(), f.owner, v.ID, domain.VacationInput{StartsAt: &now})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		var app *apperror.Error
		if !errors.As(err, &app) || app.Code != "version_conflict" {
			t.Fatalf("race: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
	a, err = uc.Get(t.Context(), f.owner, v.ID)
	if err != nil || a.Current == nil || a.Current.Status != domain.IntervalActive || a.AcceptingOrders || !a.EnforcementPending() || a.AvailabilityVersion != 1 || a.SaleVersion != v.Version+1 {
		t.Fatalf("pause: %+v %v", a, err)
	}
	var audits, events, notices int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM vendor_availability_audit_logs WHERE vendor_id=$1`, v.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM vendor_outbox WHERE vendor_id=$1 AND accepting_orders=false`, v.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FROM vendor_notification_outbox WHERE vendor_id=$1 AND type='shop_vacation_started'`, v.ID).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if audits != 2 || events != 1 || notices != 1 {
		t.Fatalf("audit/event/notice=%d/%d/%d", audits, events, notices)
	}
	uc.Enabled = false
	if _, err := uc.Schedule(t.Context(), f.owner, v.ID, domain.VacationInput{ExpectedVersion: 1}); err == nil {
		t.Fatal("flag off allowed schedule")
	}
	a, err = uc.Resume(t.Context(), f.owner, v.ID, 1)
	if err != nil || !a.AcceptingOrders || a.Current != nil {
		t.Fatalf("rollback resume: %+v %v", a, err)
	}
}

func TestVacationSchedulerBoundariesRestartAndSuspension(t *testing.T) {
	f := setup(t)
	v := approvedVacationShop(t, f)
	now := time.Now().UTC().Truncate(time.Microsecond)
	start, end := now.Add(time.Hour), now.Add(2*time.Hour)
	uc := vacationUseCase(f, now)
	if _, err := uc.Schedule(t.Context(), f.owner, v.ID, domain.VacationInput{StartsAt: &start, EndsAt: &end}); err != nil {
		t.Fatal(err)
	}
	// A fresh worker has no in-memory schedule; the stored interval drives it.
	for _, tc := range []struct {
		at        time.Time
		accepting bool
		version   int64
	}{
		{start.Add(-time.Microsecond), true, v.Version}, {start, false, v.Version + 1}, {end.Add(-time.Microsecond), false, v.Version + 1},
	} {
		worker := vacationUseCase(f, tc.at)
		worker.Enabled = false
		if err := worker.Advance(t.Context(), v.ID); err != nil {
			t.Fatal(err)
		}
		a, err := worker.Get(t.Context(), f.owner, v.ID)
		if err != nil || a.AcceptingOrders != tc.accepting || a.SaleVersion != tc.version {
			t.Fatalf("at %v: %+v %v", tc.at, a, err)
		}
	}
	if _, err := f.db.Exec(t.Context(), `UPDATE vendors SET status='suspended', version=version+1 WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	worker := vacationUseCase(f, end)
	worker.Enabled = false
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := worker.Advance(t.Context(), v.ID); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	a, err := worker.Get(t.Context(), f.owner, v.ID)
	if err != nil || a.Status != domain.StatusSuspended || !a.AcceptingOrders || a.Current != nil || a.SaleVersion != v.Version+3 {
		t.Fatalf("end lifted suspension or repeated transition: %+v %v", a, err)
	}
	// A worker that missed the entire interval converges on subsequent passes.
	v2 := approvedVacationShop(t, f)
	if _, err := uc.Schedule(t.Context(), f.owner, v2.ID, domain.VacationInput{StartsAt: &start, EndsAt: &end}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := worker.Advance(t.Context(), v2.ID); err != nil {
			t.Fatal(err)
		}
	}
	a, err = worker.Get(t.Context(), f.owner, v2.ID)
	if err != nil || !a.AcceptingOrders || a.Current != nil {
		t.Fatalf("missed interval: %+v %v", a, err)
	}
}

func TestVacationRoutesPermissionAndLegacySaleStatus(t *testing.T) {
	f := setup(t)
	v := approvedVacationShop(t, f)
	uc := vacationUseCase(f, time.Now().UTC())
	staff := uc.Access.(*usecase.StaffUseCase)
	clerk := uuid.NewString()
	f.ops.Actors.(actors)[clerk] = "buyer"
	mail := staff.Mailer.(*capturedMail)
	if _, err := staff.Invite(t.Context(), f.owner, v.ID, usecase.InviteInput{Email: clerk + "@example.test", Permissions: []string{shopaccess.ShopAvailabilityWrite}}); err != nil {
		t.Fatal(err)
	}
	if !staff.DeliverNextInvitation(t.Context()) {
		t.Fatal("invitation not delivered")
	}
	if _, err := staff.AcceptInvitation(t.Context(), clerk, mail.token(t)); err != nil {
		t.Fatal(err)
	}
	jwt := authjwttest.Manager()
	r := gin.New()
	transport.AvailabilityHandler{UseCase: uc, Log: zerolog.Nop()}.Register(r, middleware.RequireAuth(jwt))
	r.GET("/internal/vendors/sale-status", transport.NewInternalHandler(f.uc, zerolog.Nop()).SaleStatus)
	call := func(actor, role, method, suffix, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/vendor/"+v.ID+"/availability"+suffix, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if actor != "" {
			token, _, err := jwt.IssueAccessToken(actor, role, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		actor, role, method, suffix, body string
		status                            int
	}{
		{"", "", "GET", "", "", 401},
		{f.other, "vendor", "PUT", "", `{"expected_version":0}`, 403},
		{f.owner, "admin", "PUT", "", `{"expected_version":0}`, 403},
		{f.owner, "vendor", "PUT", "", `{"expected_version":0,"starts_at":"2026-10-14T08:00:00"}`, 422},
		{f.owner, "vendor", "PUT", "", `{}`, 400},
		{f.owner, "vendor", "DELETE", "?expected_version=-1", "", 400},
		{clerk, "buyer", "PUT", "", `{"expected_version":0}`, 202},
	} {
		w := call(tc.actor, tc.role, tc.method, tc.suffix, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.role, tc.method, w.Code, w.Body.String())
		}
	}
	for _, aware := range []bool{false, true} {
		for _, query := range []string{"ids=" + v.ID, "after="} {
			if aware {
				query += "&availability=1"
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/internal/vendors/sale-status?"+query, nil))
			var out struct{ Data []vendorsales.Status }
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
				t.Fatalf("sale status: %d %s", w.Code, w.Body.String())
			}
			if aware && (len(out.Data) != 1 || !out.Data[0].Paused()) || !aware && len(out.Data) != 0 {
				t.Fatalf("aware=%v: %+v", aware, out.Data)
			}
		}
	}
	m, err := staff.GetMember(t.Context(), f.owner, v.ID, clerk)
	if err != nil {
		t.Fatal(err)
	}
	if err := staff.RemoveMember(t.Context(), f.owner, v.ID, clerk, m.Version, nil); err != nil {
		t.Fatal(err)
	}
	if w := call(clerk, "buyer", "DELETE", "?expected_version=1", ""); w.Code != 403 {
		t.Fatalf("revoked staff: %d", w.Code)
	}
	if w := call(f.owner, "vendor", "DELETE", fmt.Sprintf("?expected_version=%d", 1), ""); w.Code != 202 {
		t.Fatalf("owner resume: %d %s", w.Code, w.Body.String())
	}
}
