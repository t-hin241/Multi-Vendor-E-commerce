package domain

// ResetMessage exists only in memory during delivery; never persist or log it.
type ResetMessage struct {
	Email string `json:"email"`
	URL   string `json:"url"`
	// Kind: password_reset (or empty, older Identity), or
	// email_verification (PW-022).
	Kind string `json:"kind"`
}
