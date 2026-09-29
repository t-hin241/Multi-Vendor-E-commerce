package domain

// ResetMessage exists only in memory during delivery; never persist or log it.
type ResetMessage struct {
	Email string `json:"email"`
	URL   string `json:"url"`
}
