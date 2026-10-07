// Package adminaccesstest provides permission checkers for router tests.
package adminaccesstest

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
)

// Guard is a guard that grants every bundle, for router tests that are
// not about permissions.
func Guard(routes adminaccess.Routes) gin.HandlerFunc {
	return adminaccess.Guard(&Checker{}, routes, zerolog.Nop())
}

// AssertCovered fails for every registered admin route (a path with an
// "/admin" segment, or one of extra) that the table does not name, so a
// new admin route cannot ship without a permission.
func AssertCovered(t *testing.T, r *gin.Engine, routes adminaccess.Routes, extra ...string) {
	t.Helper()
	for _, rt := range r.Routes() {
		key := rt.Method + " " + rt.Path
		admin := strings.Contains(rt.Path, "/admin/") || strings.HasSuffix(rt.Path, "/admin")
		for _, e := range extra {
			admin = admin || key == e
		}
		if admin {
			if _, ok := routes[key]; !ok {
				t.Errorf("admin route %s has no permission in the table", key)
			}
		}
	}
	for _, e := range extra {
		if _, ok := routes[e]; !ok {
			t.Errorf("admin route %s has no permission in the table", e)
		}
	}
}

// Checker grants the listed bundles to everyone (all bundles when Grants
// is nil) and records what was asked.
type Checker struct {
	Grants []string
	mu     sync.Mutex
	Asked  []string
}

func (c *Checker) Require(_ context.Context, _ string, permission string) (int64, error) {
	c.mu.Lock()
	c.Asked = append(c.Asked, permission)
	c.mu.Unlock()
	if c.Grants == nil {
		return 1, nil
	}
	for _, g := range c.Grants {
		if g == permission {
			return 1, nil
		}
	}
	return 0, adminaccess.Missing(permission)
}
