package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Review admin route
// (AF-19); a route missing here is refused.
var AdminRoutes = adminaccess.Routes{
	"GET /api/reviews/admin":                          adminaccess.ModerationManage,
	"GET /api/reviews/admin/operations":               adminaccess.ModerationManage,
	"POST /api/reviews/admin/:id/hide":                adminaccess.ModerationManage,
	"POST /api/reviews/admin/:id/restore":             adminaccess.ModerationManage,
	"GET /api/reviews/admin/reports":                  adminaccess.ModerationManage,
	"POST /api/reviews/admin/reports/:id/resolve":     adminaccess.ModerationManage,
	"GET /api/reviews/admin/moderation-reasons":       adminaccess.PlatformConfigure,
	"POST /api/reviews/admin/moderation-reasons":      adminaccess.PlatformConfigure,
	"PATCH /api/reviews/admin/moderation-reasons/:id": adminaccess.PlatformConfigure,
}.Merge(adminaccess.Shared("/api/reviews/admin", true, false, false))
