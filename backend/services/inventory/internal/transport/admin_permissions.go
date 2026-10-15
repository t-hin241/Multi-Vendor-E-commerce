package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Inventory admin route
// (AF-19); a route missing here is refused.
var AdminRoutes = adminaccess.Routes{
	"GET /api/inventory/admin/stock-alerts":                     adminaccess.PlatformConfigure,
	"POST /api/inventory/admin/stock-alerts/:id/retry":          adminaccess.PlatformConfigure,
	"GET /api/inventory/admin/vendors/:id/self-service-policy":  adminaccess.PlatformConfigure,
	"POST /api/inventory/admin/vendors/:id/self-service-policy": adminaccess.PlatformConfigure,
	"GET /api/inventory/admin/stock-adjustments":                adminaccess.ModerationManage,
	"POST /api/inventory/admin/stock-adjustments/:id/decisions": adminaccess.ModerationManage,
	"GET /api/inventory/admin/restock-requests":                 adminaccess.ModerationManage,
	"PATCH /api/inventory/admin/restock-requests/:id/approve":   adminaccess.ModerationManage,
	"PATCH /api/inventory/admin/restock-requests/:id/reject":    adminaccess.ModerationManage,
	"GET /api/inventory/admin/operations":                       adminaccess.PlatformConfigure,
	"GET /api/inventory/admin/operations/issues":                adminaccess.PlatformConfigure,
	"POST /api/inventory/admin/operations/repair":               adminaccess.PlatformConfigure,
	// AF-16: operators watch shops' imports and stop them during a rollback.
	"GET /api/inventory/admin/import-jobs":                    adminaccess.ModerationManage,
	"POST /api/inventory/admin/import-jobs/:id/cancellations": adminaccess.ModerationManage,
}.Merge(adminaccess.Shared("/api/inventory/admin", true, false, false))
