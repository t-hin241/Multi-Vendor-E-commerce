package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Shipment admin route
// (AF-19); a route missing here is refused.
var AdminRoutes = adminaccess.Routes{
	"POST /api/shipments/admin/carriers":                            adminaccess.PlatformConfigure,
	"GET /api/shipments/admin/carriers":                             adminaccess.PlatformConfigure,
	"PATCH /api/shipments/admin/carriers/:id/active":                adminaccess.PlatformConfigure,
	"POST /api/shipments/admin/zones":                               adminaccess.PlatformConfigure,
	"GET /api/shipments/admin/zones":                                adminaccess.PlatformConfigure,
	"POST /api/shipments/admin/zones/:id/provinces":                 adminaccess.PlatformConfigure,
	"GET /api/shipments/admin/zones/:id/provinces":                  adminaccess.PlatformConfigure,
	"POST /api/shipments/admin/fee-rules":                           adminaccess.PlatformConfigure,
	"GET /api/shipments/admin/fee-rules":                            adminaccess.PlatformConfigure,
	"GET /api/shipments/admin/operations":                           adminaccess.SupportManage,
	"GET /api/shipments/admin/shipments/:id":                        adminaccess.SupportManage,
	"POST /api/shipments/admin/shipments/:id/deliver":               adminaccess.SupportManage,
	"POST /api/shipments/admin/shipments/:id/failed-attempts":       adminaccess.SupportManage,
	"POST /api/shipments/admin/shipments/:id/return":                adminaccess.SupportManage,
	"POST /api/shipments/admin/shipments/:id/failure-reports":       adminaccess.SupportManage,
	"POST /api/shipments/admin/shipments/:id/tracking":              adminaccess.SupportManage,
	"POST /api/shipments/admin/shipments/:id/interception-decision": adminaccess.SupportManage,
	"POST /api/shipments/admin/order-events/:id/retry":              adminaccess.PlatformConfigure,
	"GET /api/shipments/admin/return-shipments":                     adminaccess.SupportManage,
}.Merge(adminaccess.Shared("/api/shipments/admin", true, true, true))
