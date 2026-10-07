package casesla

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

type fakeRepo struct{ calls int }

func (r *fakeRepo) List(context.Context, Filter, time.Time) (*Page, error) {
	r.calls++
	return &Page{}, nil
}
func (r *fakeRepo) Mutate(context.Context, string, string, string, string, Mutation, time.Time) (*Item, error) {
	r.calls++
	return &Item{}, nil
}

func TestRevokedAdminCannotReadOrMutate(t *testing.T) {
	r := &fakeRepo{}
	s := Service{Repo: r, Roles: rejectRole{}}
	if _, e := s.List(t.Context(), "buyer", Filter{Limit: 20}); e == nil {
		t.Fatal("unauthorized read")
	}
	if _, e := s.Mutate(t.Context(), "buyer", "id", "extensions", "test-key", Mutation{}); e == nil {
		t.Fatal("unauthorized write")
	}
	if r.calls != 0 {
		t.Fatal("unauthorized repository access")
	}
}
func TestReasonVersionAndKeyRequired(t *testing.T) {
	r := &fakeRepo{}
	s := Service{Repo: r, Roles: allowRole{}}
	if _, e := s.Mutate(t.Context(), "admin", "00000000-0000-0000-0000-000000000001", "extensions", "", Mutation{}); e == nil {
		t.Fatal("unreviewable mutation")
	}
	if r.calls != 0 {
		t.Fatal("invalid command reached store")
	}
}
func TestOwnerHandlerRejectsNonAdminBeforeDataAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	repo := &fakeRepo{}
	Register(r.Group("/api/orders/admin"), Service{Repo: repo, Roles: rejectRole{}}, zerolog.Nop())
	for _, method := range []string{"GET", "POST"} {
		path := "/api/orders/admin/work-items"
		if method == "POST" {
			path += "/00000000-0000-0000-0000-000000000001/extensions"
		}
		req := httptest.NewRequest(method, path, strings.NewReader(`{"expected_version":1,"reason":"test reason"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s returned %d: %s", method, w.Code, w.Body.String())
		}
	}
	if repo.calls != 0 {
		t.Fatal("forbidden handler accessed data")
	}
}
