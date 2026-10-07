package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Admin BFF route
// (AF-19). The BFF only reads; each owner service it forwards to checks
// its own bundle again with the caller's credentials.
var AdminRoutes = adminaccess.Routes{
	"GET /api/admin/dashboard":  adminaccess.AnalyticsRead,
	"GET /api/admin/audit":      adminaccess.AuditRead,
	"GET /api/admin/work-items": adminaccess.SupportManage,
}
