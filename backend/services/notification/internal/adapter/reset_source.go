package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/services/notification/internal/domain"
)

type ResetSource struct{ URL, Key string }

func (s ResetSource) GetResetMessage(ctx context.Context, id string) (*domain.ResetMessage, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", s.URL+"/internal/password-reset-deliveries/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, fmt.Errorf("reset source unavailable")
	}
	req.Header.Set("X-Reset-Delivery-Key", s.Key)
	req.Header.Set("X-Request-ID", id)
	res, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("reset source unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("reset message unavailable")
	}
	var message domain.ResetMessage
	err = json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&message)
	return &message, err
}
