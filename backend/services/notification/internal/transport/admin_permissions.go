package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Notification admin
// route (AF-19); a route missing here is refused.
var AdminRoutes = adminaccess.Routes{
	"GET /api/notifications/admin":              adminaccess.SupportManage,
	"GET /api/notifications/admin/operations":   adminaccess.SupportManage,
	"GET /api/notifications/admin/:id/attempts": adminaccess.SupportManage,
	"POST /api/notifications/admin/:id/retry":   adminaccess.SupportManage,
	// AF-08: shop notice events without recipients and their retry.
	"GET /api/notifications/admin/vendor-actions":            adminaccess.SupportManage,
	"GET /api/notifications/admin/vendor-actions/summary":    adminaccess.SupportManage,
	"POST /api/notifications/admin/vendor-actions/:id/retry": adminaccess.SupportManage,
}.Merge(adminaccess.Shared("/api/notifications/admin", true, true, true))
