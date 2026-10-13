package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/inventory/internal/repository"
)

type StockAlertClient struct{ URL, Key string }

func (c StockAlertClient) Send(ctx context.Context, a repository.StockAlert) error {
	payload, err := json.Marshal(map[string]string{"source": "inventory", "event_id": a.ID, "vendor_id": a.VendorID, "action_kind": "low_stock", "reference_id": a.ItemID, "correlation_id": a.ID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/internal/vendor-action-notices", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", a.ID)
	res, err := telemetry.NewHTTPClient(2 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("deliver stock alert: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	return &apperror.Error{Status: res.StatusCode, Code: "stock_alert_refused", Message: "Notification refused stock alert"}
}
