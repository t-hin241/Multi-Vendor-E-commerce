package transport

import "shopee/backend/pkg/adminaccess"

// AdminRoutes names the permission bundle of every Payment admin route
// (AF-19); a route missing here is refused. Recording money results and
// ledger adjustments is finance.prepare; with approvals on, the direct
// routes answer 409 approval_required and only finance.approve executes.
// AF-06: verifying a refund destination and confirming a manual transfer
// are finance.approve; reading a destination in full is finance.prepare
// for the claimer (anyone else also needs finance.approve, checked by
// the use case).
var AdminRoutes = adminaccess.Routes{
	"GET /api/payments/admin/refunds": adminaccess.FinanceRead,
	// PW-032: reimbursements (a second admin approves).
	"GET /api/payments/admin/reimbursements":                        adminaccess.FinanceRead,
	"POST /api/payments/admin/reimbursements":                       adminaccess.FinancePrepare,
	"POST /api/payments/admin/reimbursements/:id/decisions":         adminaccess.FinanceApprove,
	"POST /api/payments/admin/reimbursements/:id/payments":          adminaccess.FinancePrepare,
	"GET /api/payments/admin/refunds/:id":                           adminaccess.FinanceRead,
	"POST /api/payments/admin/refunds/:id/resolve":                  adminaccess.FinancePrepare,
	"GET /api/payments/admin/refunds/:id/manual":                    adminaccess.FinanceRead,
	"POST /api/payments/admin/refunds/:id/destination-decisions":    adminaccess.FinanceApprove,
	"POST /api/payments/admin/refunds/:id/sensitive-access":         adminaccess.FinancePrepare,
	"POST /api/payments/admin/refunds/:id/manual-attempts":          adminaccess.FinancePrepare,
	"POST /api/payments/admin/refund-attempts/:id/claims":           adminaccess.FinancePrepare,
	"POST /api/payments/admin/refund-attempts/:id/cancellation":     adminaccess.FinancePrepare,
	"POST /api/payments/admin/refund-attempts/:id/evidence":         adminaccess.FinancePrepare,
	"POST /api/payments/admin/refund-attempts/:id/submissions":      adminaccess.FinancePrepare,
	"POST /api/payments/admin/refund-attempts/:id/decisions":        adminaccess.FinanceApprove,
	"GET /api/payments/admin/refund-evidence/:id":                   adminaccess.FinanceRead,
	"GET /api/payments/admin/reconciliation":                        adminaccess.FinanceRead,
	"GET /api/payments/admin/search":                                adminaccess.FinanceRead,
	"GET /api/payments/admin/audit":                                 adminaccess.FinanceRead,
	"POST /api/payments/admin/receipts/:id/retry":                   adminaccess.FinancePrepare,
	"POST /api/payments/admin/intents/:id/reconcile":                adminaccess.FinancePrepare,
	"POST /api/payments/admin/order-sync/:id/retry":                 adminaccess.FinancePrepare,
	"POST /api/payments/admin/refund-sync/:id/retry":                adminaccess.FinancePrepare,
	"GET /api/payments/admin/settlements/balances":                  adminaccess.FinanceRead,
	"GET /api/payments/admin/settlements/vendors/:vendorId/entries": adminaccess.FinanceRead,
	"POST /api/payments/admin/settlements/adjustments":              adminaccess.FinancePrepare,
	"GET /api/payments/admin/payouts/batches":                       adminaccess.FinanceRead,
	"POST /api/payments/admin/payouts/batches":                      adminaccess.FinancePrepare,
	"GET /api/payments/admin/payouts/batches/:id":                   adminaccess.FinanceRead,
	"POST /api/payments/admin/payouts/items/:id/resolve":            adminaccess.FinancePrepare,
	"POST /api/payments/admin/payouts/items/:id/cancellation":       adminaccess.FinancePrepare,
	"GET /api/payments/admin/settlement-holds":                      adminaccess.FinanceRead,
	"GET /api/payments/admin/approval-requests":                     adminaccess.FinanceRead,
	"GET /api/payments/admin/approval-requests/:id":                 adminaccess.FinanceRead,
	"POST /api/payments/admin/approval-requests":                    adminaccess.FinancePrepare,
	"POST /api/payments/admin/approval-requests/:id/submission":     adminaccess.FinancePrepare,
	"POST /api/payments/admin/approval-requests/:id/cancellation":   adminaccess.FinancePrepare,
	"POST /api/payments/admin/approval-requests/:id/decisions":      adminaccess.FinanceApprove,
}.Merge(adminaccess.Shared("/api/payments/admin", true, true, true))
