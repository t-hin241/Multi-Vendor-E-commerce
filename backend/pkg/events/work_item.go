package events

import "time"

const (
	OrderWorkItemReminder    = "order.work_item_reminder"
	OrderWorkItemOverdue     = "order.work_item_overdue"
	PaymentWorkItemReminder  = "payment.work_item_reminder"
	PaymentWorkItemOverdue   = "payment.work_item_overdue"
	ShipmentWorkItemReminder = "shipment.work_item_reminder"
	ShipmentWorkItemOverdue  = "shipment.work_item_overdue"
)

// WorkItemNotice is a deadline fact, never a command to resolve a case.
// One stable event per recipient is retried from the owner's outbox.
type WorkItemNotice struct {
	ResourceType    string    `json:"resource_type"`
	ResourceID      string    `json:"resource_id"`
	SLAVersion      string    `json:"sla_version"`
	DeadlineVersion int64     `json:"deadline_version"`
	Stage           string    `json:"stage"`
	DueAt           time.Time `json:"due_at"`
	Kind            string    `json:"kind"`
	UserID          string    `json:"user_id"`
}
