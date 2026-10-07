package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// StaffInvitationMailer asks Notification to send one invitation email
// now. The link (with its one-time token) only travels in this request
// body: neither side stores or logs it.
type StaffInvitationMailer struct {
	URL, Key string
	client   *http.Client
}

func NewStaffInvitationMailer(notificationURL, key string) *StaffInvitationMailer {
	return &StaffInvitationMailer{URL: strings.TrimRight(notificationURL, "/") + "/internal/notifications/staff-invitations", Key: key,
		client: telemetry.NewHTTPClient(25 * time.Second)}
}

func (m *StaffInvitationMailer) SendInvitation(ctx context.Context, mail usecase.InvitationMail) error {
	body, err := json.Marshal(map[string]any{"delivery_id": mail.DeliveryID, "email": mail.Email, "shop_name": mail.ShopName,
		"url": mail.URL, "expires_at": mail.ExpiresAt.UTC()})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, m.Key)
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("invitation notification unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("invitation notification failed: status %d", resp.StatusCode)
	}
	return nil
}
