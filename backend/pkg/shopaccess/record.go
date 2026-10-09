package shopaccess

import (
	"context"
	"sync"

	"github.com/gin-gonic/gin"
)

// PW-021: a seller command is authorized at the start of the request, and
// a revoke can land before its transaction commits. Calling Vendor again
// inside the transaction is not allowed (no remote call while a database
// transaction is open), so the owning service records which grant allowed
// the change instead: every successful Authorize in the request is kept in
// its context, and the audit rows of sensitive commands store the grant's
// membership version. Vendor's membership audit then shows whether that
// version had already been revoked.

type grantsKey struct{}

type grants struct {
	mu   sync.Mutex
	last *Grant
}

// WithGrants returns ctx carrying a place for the grants its request uses.
func WithGrants(ctx context.Context) context.Context {
	if _, ok := ctx.Value(grantsKey{}).(*grants); ok {
		return ctx
	}
	return context.WithValue(ctx, grantsKey{}, &grants{})
}

// RecordGrants is the router middleware that gives every request a place
// for its grants.
func RecordGrants() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(WithGrants(c.Request.Context()))
		c.Next()
	}
}

func remember(ctx context.Context, g Grant) {
	if r, ok := ctx.Value(grantsKey{}).(*grants); ok {
		r.mu.Lock()
		r.last = &g
		r.mu.Unlock()
	}
}

// UsedGrant is the last grant this request was authorized with.
func UsedGrant(ctx context.Context) (Grant, bool) {
	r, ok := ctx.Value(grantsKey{}).(*grants)
	if !ok {
		return Grant{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return Grant{}, false
	}
	return *r.last, true
}

// UsedMembershipVersion is the membership version to store in an audit
// row, or nil when the change was not made under a shop grant (admin,
// system, a service).
func UsedMembershipVersion(ctx context.Context) *int64 {
	g, ok := UsedGrant(ctx)
	if !ok {
		return nil
	}
	v := g.MembershipVersion
	return &v
}
