package domain

type OperationsSummary struct {
	PendingApplications       int64   `json:"pending_applications"`
	PendingStatusEvents       int64   `json:"pending_status_events"`
	ParkedStatusEvents        int64   `json:"parked_status_events"`
	OldestPendingEventSeconds float64 `json:"oldest_pending_event_seconds"`
	PayoutChanges24Hours      int64   `json:"payout_changes_24_hours"`
	PendingNotices            int64   `json:"pending_notices"`
	ParkedNotices             int64   `json:"parked_notices"`
}
