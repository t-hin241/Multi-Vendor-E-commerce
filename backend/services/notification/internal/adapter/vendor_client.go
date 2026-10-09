package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

// NoticeRecipient is one person Vendor says may receive a shop notice.
type NoticeRecipient struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// NoticeRecipients is Vendor's answer for one shop and purpose.
type NoticeRecipients struct {
	Recipients        []NoticeRecipient `json:"recipients"`
	PermissionVersion string            `json:"permission_version"`
}

// VendorClient reads a shop's notice recipients (AF-08). Notification owns
// no membership data: who may be told is asked at resolution time.
type VendorClient struct {
	baseURL    string
	credential string
	client     *http.Client
}

func NewVendorClient(baseURL, credential string) *VendorClient {
	return &VendorClient{baseURL: baseURL, credential: credential, client: telemetry.NewHTTPClient(5 * time.Second)}
}

// NotificationRecipients calls GET /internal/vendors/:id/notification-recipients.
// A missing shop is NotFound (nobody to tell); anything else that is not
// an answer is an error to retry, never an empty list.
func (c *VendorClient) NotificationRecipients(ctx context.Context, vendorID, purpose string) (*NoticeRecipients, error) {
	endpoint := fmt.Sprintf("%s/internal/vendors/%s/notification-recipients?purpose=%s", c.baseURL, url.PathEscape(vendorID), url.QueryEscape(purpose))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	serviceauth.SetRequestHeaders(req, c.credential)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, apperror.NotFound("Shop not found")
	case resp.StatusCode != http.StatusOK:
		return nil, apperror.Internal(fmt.Errorf("vendor service returned status %d", resp.StatusCode))
	}
	var body struct {
		Data NoticeRecipients `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&body); err != nil {
		return nil, apperror.Internal(err)
	}
	if body.Data.Recipients == nil {
		return nil, apperror.Internal(fmt.Errorf("vendor service answered without a recipient list"))
	}
	return &body.Data, nil
}
