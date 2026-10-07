package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Catalog admin route
// (AF-19); a route missing here is refused.
var AdminRoutes = adminaccess.Routes{
	"POST /api/catalog/categories":                     adminaccess.PlatformConfigure,
	"GET /api/catalog/attributes":                      adminaccess.PlatformConfigure,
	"POST /api/catalog/attributes":                     adminaccess.PlatformConfigure,
	"POST /api/catalog/attributes/:id/options":         adminaccess.PlatformConfigure,
	"POST /api/catalog/categories/:id/attribute-rules": adminaccess.PlatformConfigure,
	"GET /api/catalog/products/admin":                  adminaccess.ModerationManage,
	"GET /api/catalog/products/admin/:id":              adminaccess.ModerationManage,
	"GET /api/catalog/products/admin/:id/audit-log":    adminaccess.ModerationManage,
	"PATCH /api/catalog/products/admin/:id/approve":    adminaccess.ModerationManage,
	"PATCH /api/catalog/products/admin/:id/reject":     adminaccess.ModerationManage,
	"GET /api/catalog/operations":                      adminaccess.PlatformConfigure,
	"POST /api/catalog/operations/replay":              adminaccess.PlatformConfigure,
}.Merge(adminaccess.Shared("/api/catalog/admin", true, false, true))

// adminOnlyRoutes are admin routes without an "/admin" path segment.
var adminOnlyRoutes = []string{"POST /api/catalog/categories", "GET /api/catalog/attributes", "POST /api/catalog/attributes",
	"POST /api/catalog/attributes/:id/options", "POST /api/catalog/categories/:id/attribute-rules"}
