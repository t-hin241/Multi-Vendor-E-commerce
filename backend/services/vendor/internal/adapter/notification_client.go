package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// DispatchNotices hands queued shop-decision notices to Notification with
// the internal key. Notification answers 202 once it has recorded the
// notice (the row id is its event id, so a resend is a duplicate there). A
// 4xx answer parks the notice; anything else is retried with backoff.
func DispatchNotices(ctx context.Context, outbox repository.NotificationOutbox, notificationURL, key string, log zerolog.Logger) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	client := &http.Client{Timeout: 5 * time.Second}
	target := strings.TrimRight(notificationURL, "/") + "/internal/notifications"
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		batch, err := outbox.Claim(ctx)
		if err != nil {
			log.Error().Msg("vendor_notice_claim_failed")
			continue
		}
		for _, n := range batch {
			reason, refused := sendNotice(ctx, client, target, key, n)
			if reason == "" {
				if err := outbox.Delivered(ctx, n.ID); err != nil {
					log.Error().Str("notice_id", n.ID).Msg("vendor_notice_complete_failed")
				}
				continue
			}
			if err := outbox.Failed(ctx, n, reason, refused); err != nil {
				log.Error().Str("notice_id", n.ID).Msg("vendor_notice_complete_failed")
			}
			log.Warn().Str("notice_id", n.ID).Str("vendor_id", n.VendorID).Str("reason", reason).Int("attempt", n.Attempts).
				Bool("parked", refused || n.Attempts >= repository.MaxNoticeAttempts).Msg("vendor_notice_pending")
		}
	}
}

// sendNotice returns "" on success, otherwise a short reason and whether
// Notification refused the notice (4xx).
func sendNotice(ctx context.Context, client *http.Client, target, key string, n repository.Notice) (string, bool) {
	payload, err := json.Marshal(map[string]string{
		"event_id": n.ID, "source": "vendor", "user_id": n.UserID, "type": n.Type, "reference_id": n.VendorID,
	})
	if err != nil {
		return "encode failed", true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return "request failed", true
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, key)
	resp, err := client.Do(req)
	if err != nil {
		return "notification unreachable", false
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return "", false
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden,
		resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode == http.StatusTooManyRequests:
		// Key or capacity problems are fixed on the other side; keep trying.
		return "notification answered " + http.StatusText(resp.StatusCode), false
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return "notification refused with " + http.StatusText(resp.StatusCode), true
	}
	return "notification returned " + http.StatusText(resp.StatusCode), false
}
