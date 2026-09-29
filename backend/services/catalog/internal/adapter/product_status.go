package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"shopee/backend/pkg/productsales"
	"shopee/backend/pkg/serviceauth"
	"strings"
	"time"
)

type ProductStatusPublisher struct{ URL, Key string }

func (p ProductStatusPublisher) Publish(ctx context.Context, v productsales.Status) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.URL, "/")+"/internal/product-status", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	serviceauth.SetRequestHeaders(req, p.Key)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("order product status delivery unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("order product status delivery returned %d", resp.StatusCode)
	}
	return nil
}
