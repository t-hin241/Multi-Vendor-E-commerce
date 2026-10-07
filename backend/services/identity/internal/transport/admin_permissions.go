package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Identity admin route
// (AF-19); a route missing here is refused.
var AdminRoutes = adminaccess.Routes{
	"GET /api/auth/admin/users":                                 adminaccess.ModerationManage,
	"PATCH /api/auth/admin/users/:id/active":                    adminaccess.ModerationManage,
	"POST /api/auth/admin/users/:id/sessions/:sessionID/revoke": adminaccess.AccessManage,
	"GET /api/auth/admin/permission-subjects":                   adminaccess.AccessManage,
	"GET /api/auth/admin/permission-grants":                     adminaccess.AccessManage,
	"POST /api/auth/admin/permission-grants":                    adminaccess.AccessManage,
	"DELETE /api/auth/admin/permission-grants/:id":              adminaccess.AccessManage,
}.Merge(adminaccess.Shared("/api/auth/admin", true, false, false))
