package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/vendorreport"
)

type ReportClient struct{ URL, Key string }

func (c ReportClient) Report(ctx context.Context, id string, r vendorreport.Range) (*vendorreport.Report, error) {
	q := url.Values{"from": {r.From.Format(time.RFC3339)}, "to": {r.To.Format(time.RFC3339)}, "currency": {r.Currency}}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(c.URL, "/")+"/internal/vendor-reports/"+url.PathEscape(id)+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("report service unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("report service returned %d", resp.StatusCode)
	}
	var data struct {
		Data vendorreport.Report `json:"data"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&data); err != nil {
		return nil, err
	}
	return &data.Data, nil
}
