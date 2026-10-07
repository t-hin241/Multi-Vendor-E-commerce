package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Inventory admin route
// (AF-19); a route missing here is refused.
var AdminRoutes = adminaccess.Routes{
	"GET /api/inventory/admin/restock-requests":               adminaccess.ModerationManage,
	"PATCH /api/inventory/admin/restock-requests/:id/approve": adminaccess.ModerationManage,
	"PATCH /api/inventory/admin/restock-requests/:id/reject":  adminaccess.ModerationManage,
	"GET /api/inventory/admin/operations":                     adminaccess.PlatformConfigure,
	"GET /api/inventory/admin/operations/issues":              adminaccess.PlatformConfigure,
	"POST /api/inventory/admin/operations/repair":             adminaccess.PlatformConfigure,
}.Merge(adminaccess.Shared("/api/inventory/admin", true, false, false))
