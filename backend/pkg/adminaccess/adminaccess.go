// Package adminaccess is the scoped admin permission contract (AF-19).
//
// The admin role alone no longer decides what an operator may do: Identity
// keeps permission bundles per admin, and every admin route names the
// bundle it needs in a per-service table. A route missing from the table is
// refused (deny by default). Identity answers per request; nothing caches a
// positive answer. An unknown answer is 503 auth_unavailable, a refusal 403
// missing_permission. While Identity's FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED
// is off, every active admin holds every bundle (the behaviour before AF-19).
package adminaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

// Permission bundles (v1).
const (
	SupportManage     = "support.manage"
	ModerationManage  = "moderation.manage"
	FinanceRead       = "finance.read"
	FinancePrepare    = "finance.prepare"
	FinanceApprove    = "finance.approve"
	PlatformConfigure = "platform.configure"
	AnalyticsRead     = "analytics.read"
	AuditRead         = "audit.read"
	AccessManage      = "access.manage"
)

var bundles = []string{SupportManage, ModerationManage, FinanceRead, FinancePrepare, FinanceApprove, PlatformConfigure,
	AnalyticsRead, AuditRead, AccessManage}

// Bundles returns the bundle registry.
func Bundles() []string { return slices.Clone(bundles) }

// Known reports whether name is a bundle.
func Known(name string) bool { return slices.Contains(bundles, name) }

const (
	CodeMissingPermission apperror.Code = "missing_permission"
	CodeAuthUnavailable   apperror.Code = "auth_unavailable"
	CodeReauthRequired    apperror.Code = "reauthentication_required"
)

// Missing is the refusal (403 missing_permission).
func Missing(permission string) *apperror.Error {
	return &apperror.Error{Code: CodeMissingPermission, Message: "Your admin account lacks the " + permission + " permission",
		Status: http.StatusForbidden}
}

// Unavailable is the fail-closed answer when permissions cannot be read.
func Unavailable(err error) *apperror.Error {
	return &apperror.Error{Code: CodeAuthUnavailable, Message: "Admin permissions cannot be checked right now; please try again shortly",
		Status: http.StatusServiceUnavailable, Err: err}
}

// ReauthRequired is a missing, used, expired or mismatched proof.
func ReauthRequired() *apperror.Error {
	return &apperror.Error{Code: CodeReauthRequired, Message: "Confirm your password again for this action",
		Status: http.StatusForbidden}
}

// Checker answers whether an admin holds a bundle now, and with which
// permission version.
type Checker interface {
	Require(ctx context.Context, userID, permission string) (int64, error)
}

// Client calls Identity's internal permission and proof endpoints.
type Client struct {
	URL, Key string
	HTTP     *http.Client
}

func (c Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return telemetry.NewHTTPClient(3 * time.Second)
}

func (c Client) post(ctx context.Context, path string, in any) (*http.Response, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := c.client().Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}

// Require returns the admin's permission version when userID is an active
// admin holding permission, Missing when not, Unavailable on doubt.
func (c Client) Require(ctx context.Context, userID, permission string) (int64, error) {
	if _, err := uuid.Parse(userID); err != nil || !Known(permission) {
		return 0, Missing(permission)
	}
	resp, err := c.post(ctx, "/internal/admin-permissions/check", map[string]string{"user_id": userID, "permission": permission})
	if err != nil {
		return 0, Unavailable(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, Unavailable(fmt.Errorf("identity permission check answered %d", resp.StatusCode))
	}
	var out struct {
		Data struct {
			Allowed           bool   `json:"allowed"`
			UserID            string `json:"user_id"`
			PermissionVersion int64  `json:"permission_version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&out); err != nil {
		return 0, Unavailable(err)
	}
	if out.Data.UserID != userID {
		return 0, Unavailable(errors.New("identity answered for another user"))
	}
	if !out.Data.Allowed {
		return 0, Missing(permission)
	}
	return out.Data.PermissionVersion, nil
}

// ConsumeProof spends a recent-reauthentication proof of userID for one
// purpose and operation. A proof works once, within minutes of the
// password check, for exactly the operation it was made for.
func (c Client) ConsumeProof(ctx context.Context, proof, userID, purpose, operationHash string) error {
	if strings.TrimSpace(proof) == "" {
		return ReauthRequired()
	}
	resp, err := c.post(ctx, "/internal/reauth-proofs/consume", map[string]string{"proof": proof, "user_id": userID,
		"purpose": purpose, "operation_hash": operationHash})
	if err != nil {
		return Unavailable(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusConflict, http.StatusBadRequest, http.StatusForbidden:
		return ReauthRequired()
	}
	return Unavailable(fmt.Errorf("identity proof check answered %d", resp.StatusCode))
}

// Routes maps "METHOD /full/route" to the bundle it needs.
type Routes map[string]string

// Merge returns r with extra added (extra wins).
func (r Routes) Merge(extra Routes) Routes {
	out := Routes{}
	for k, v := range r {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// Shared returns the entries of the admin routes registered by shared
// packages under prefix: the audit search (pkg/adminaudit), case deadlines
// (pkg/casesla) and parked events (pkg/eventbus).
func Shared(prefix string, audit, workItems, events bool) Routes {
	out := Routes{}
	if audit {
		out["GET "+prefix+"/audit-events"] = AuditRead
	}
	if workItems {
		out["GET "+prefix+"/work-items"] = SupportManage
		for _, action := range []string{"assignments", "extensions", "activations", "notice-replays"} {
			out["POST "+prefix+"/work-items/:workItemID/"+action] = SupportManage
		}
	}
	if events {
		out["GET "+prefix+"/events/stats"] = PlatformConfigure
		out["GET "+prefix+"/events/parked"] = PlatformConfigure
		out["POST "+prefix+"/events/:consumer/:eventId/replay"] = PlatformConfigure
		out["POST "+prefix+"/events/:consumer/:eventId/discard"] = PlatformConfigure
	}
	return out
}

// Guard enforces Routes after RequireAuth and RequireRole("admin"). A
// route absent from the table is refused.
func Guard(checker Checker, routes Routes, log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.Request.Method + " " + c.FullPath()
		permission, ok := routes[key]
		if !ok || checker == nil {
			log.Warn().Str("route", key).Msg("admin_route_unmapped")
			httpresponse.HandleError(c, log, Missing("route"))
			c.Abort()
			return
		}
		version, err := checker.Require(c.Request.Context(), middleware.GetUserID(c), permission)
		if err != nil {
			var app *apperror.Error
			if errors.As(err, &app) && app.Code == CodeMissingPermission {
				log.Info().Str("route", key).Str("permission", permission).Str("actor_id", middleware.GetUserID(c)).Msg("admin_permission_denied")
			}
			httpresponse.HandleError(c, log, err)
			c.Abort()
			return
		}
		c.Set(contextKeyVersion, version)
		c.Next()
	}
}

const contextKeyVersion = "admin_permission_version"

// PermissionVersion is the version Guard checked for this request (for
// audit next to the mutation it allowed).
func PermissionVersion(c *gin.Context) int64 {
	v, _ := c.Get(contextKeyVersion)
	n, _ := v.(int64)
	return n
}
