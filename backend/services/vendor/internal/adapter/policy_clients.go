package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// RuleReadinessClient asks rule owners (by service name) whether they
// enforce a rule version: GET <owner>/internal/policy-rules/readiness.
type RuleReadinessClient struct {
	Owners map[string]string // service name → base URL
	Key    string
	client *http.Client
}

func NewRuleReadinessClient(owners map[string]string, key string) *RuleReadinessClient {
	return &RuleReadinessClient{Owners: owners, Key: key, client: telemetry.NewHTTPClient(2 * time.Second)}
}

// Readiness never reports ready on doubt: an unknown owner, a failed call
// or an unreadable answer is "not ready" with the reason, so the policy
// stays preparing.
func (c *RuleReadinessClient) Readiness(ctx context.Context, owner, key, value string) domain.RuleReadiness {
	base, ok := c.Owners[owner]
	if !ok || base == "" {
		return domain.RuleReadiness{Reason: "no readiness contract with " + owner}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	endpoint := strings.TrimRight(base, "/") + "/internal/policy-rules/readiness?key=" + url.QueryEscape(key) + "&value=" + url.QueryEscape(value)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return domain.RuleReadiness{Reason: "invalid request"}
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := c.client.Do(req)
	if err != nil {
		return domain.RuleReadiness{Reason: owner + " unreachable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return domain.RuleReadiness{Reason: fmt.Sprintf("%s answered %d", owner, resp.StatusCode)}
	}
	var body struct {
		Data domain.RuleReadiness `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return domain.RuleReadiness{Reason: "unreadable answer from " + owner}
	}
	if body.Data.Ready && body.Data.RuleHash == "" {
		return domain.RuleReadiness{Reason: owner + " acknowledged without a rule hash"}
	}
	return body.Data
}

// PolicySender delivers one queued vendor.policy_published.
type PolicySender func(ctx context.Context, d repository.PolicyDelivery) error

// BusPolicySender publishes the event (the outbox row id is the event id).
func BusPolicySender(bus *eventbus.Bus) PolicySender {
	return func(ctx context.Context, d repository.PolicyDelivery) error {
		var p events.PolicyPublication
		if err := json.Unmarshal(d.Payload, &p); err != nil {
			return err
		}
		env, err := events.PolicyPublishedEvent(d.ID, p)
		if err != nil {
			return err
		}
		return bus.Publish(ctx, env.WithCorrelation(d.ID))
	}
}

// HTTPPolicySender posts to Order directly (EVENT_PUBLISHING=http, rollback
// only).
func HTTPPolicySender(orderURL, key string) PolicySender {
	client := telemetry.NewHTTPClient(3 * time.Second)
	target := strings.TrimRight(orderURL, "/") + "/internal/policy-published"
	return func(ctx context.Context, d repository.PolicyDelivery) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(d.Payload))
		if err != nil {
			return err
		}
		serviceauth.SetRequestHeaders(req, key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Event-Id", d.ID)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("order answered %d", resp.StatusCode)
		}
		return nil
	}
}

// PolicyRetrier asks rule owners again for preparing versions.
type PolicyRetrier interface {
	RetryPreparing(ctx context.Context, limit int) (int, error)
}

// DispatchPolicies relays publication events and, every 30 seconds, asks
// the rule owners again for versions still preparing.
func DispatchPolicies(ctx context.Context, outbox repository.PolicyRepository, send PolicySender, retrier PolicyRetrier, log zerolog.Logger) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	lastRetry := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Since(lastRetry) >= 30*time.Second {
			lastRetry = time.Now()
			if _, err := retrier.RetryPreparing(ctx, 20); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Msg("vendor_policy_preparing_retry_failed")
			}
		}
		batch, err := outbox.ClaimPublications(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Error().Msg("vendor_policy_outbox_claim_failed")
			}
			continue
		}
		for _, d := range batch {
			sendErr := send(ctx, d)
			if err := outbox.CompletePublication(ctx, d, sendErr); err != nil {
				log.Error().Str("event_id", d.ID).Msg("vendor_policy_outbox_complete_failed")
			}
			if sendErr != nil {
				log.Warn().Err(sendErr).Str("event_id", d.ID).Str("policy_id", d.PolicyID).Int("attempt", d.Attempts).
					Bool("parked", d.Attempts >= repository.MaxPolicyDeliveryAttempts).Msg("vendor_policy_propagation_pending")
			}
		}
	}
}
