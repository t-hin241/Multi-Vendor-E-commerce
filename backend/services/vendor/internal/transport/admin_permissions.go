package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Vendor admin route
// (AF-19); a route missing here is refused. Verifying a payout destination
// and reading its full details are finance.approve: the shop owner
// submitted the destination, the approving admin is the second person.
var AdminRoutes = adminaccess.Routes{
	"GET /api/vendor/admin/operations":                                    adminaccess.ModerationManage,
	"GET /api/vendor/admin/applications":                                  adminaccess.ModerationManage,
	"GET /api/vendor/admin/applications/:id/audit-log":                    adminaccess.ModerationManage,
	"PATCH /api/vendor/admin/applications/:id/suspend":                    adminaccess.ModerationManage,
	"PATCH /api/vendor/admin/applications/:id/restore":                    adminaccess.ModerationManage,
	"PATCH /api/vendor/admin/applications/:id/approve":                    adminaccess.ModerationManage,
	"PATCH /api/vendor/admin/applications/:id/reject":                     adminaccess.ModerationManage,
	"POST /api/vendor/admin/applications/:id/replay":                      adminaccess.PlatformConfigure,
	"GET /api/vendor/admin/policy-versions":                               adminaccess.PlatformConfigure,
	"POST /api/vendor/admin/policy-versions":                              adminaccess.PlatformConfigure,
	"POST /api/vendor/admin/policy-versions/:id/publications":             adminaccess.PlatformConfigure,
	"POST /api/vendor/admin/policy-versions/:id/withdrawal":               adminaccess.PlatformConfigure,
	"GET /api/vendor/admin/policy-proposals":                              adminaccess.ModerationManage,
	"POST /api/vendor/admin/policy-proposals/:id/decisions":               adminaccess.ModerationManage,
	"GET /api/vendor/admin/shops/:vendorId/payout-accounts":               adminaccess.FinanceRead,
	"POST /api/vendor/admin/shops/:vendorId/payout-accounts/:id/decision": adminaccess.FinanceApprove,
	"POST /api/vendor/admin/shops/:vendorId/payout-accounts/:id/details":  adminaccess.FinanceApprove,
	"GET /api/vendor/admin/shops/:vendorId/return-destination":            adminaccess.ModerationManage,
	"POST /api/vendor/admin/shops/:vendorId/return-destination/decision":  adminaccess.ModerationManage,
}.Merge(adminaccess.Shared("/api/vendor/admin", true, false, false))
