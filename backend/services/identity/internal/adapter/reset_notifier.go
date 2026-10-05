package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"shopee/backend/pkg/telemetry"
)

type ResetNotifier struct{ BaseURL, Key string }

func (n ResetNotifier) NotifyReset(ctx context.Context, id string) error {
	body, err := json.Marshal(map[string]string{"delivery_id": id})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.BaseURL+"/internal/notifications/password-reset", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Reset-Delivery-Key", n.Key)
	req.Header.Set("X-Request-ID", id)
	res, err := telemetry.NewHTTPClient(25 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("reset notification unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 204 {
		return fmt.Errorf("reset notification failed: status %d", res.StatusCode)
	}
	return nil
}
