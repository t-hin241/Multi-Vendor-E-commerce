package domain

import "time"

// StaffInvitation is a shop staff invitation email (AF-17). Like
// ResetMessage it exists only in memory during delivery: the link carries a
// one-time token, so never persist or log it.
type StaffInvitation struct {
	Email     string
	ShopName  string
	URL       string
	ExpiresAt time.Time
}
