package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/vendorsvc/internal/repository"
)

// StatusSender delivers one queued status change.
type StatusSender func(ctx context.Context, d repository.Delivery) error

// BusStatusSender publishes vendor.status_changed (the outbox row id is the
// event id): delivered once the broker stored it; Catalog and Order apply
// it from their durable consumers.
func BusStatusSender(bus *eventbus.Bus) StatusSender {
	return func(ctx context.Context, d repository.Delivery) error {
		env, err := events.VendorStatus(d.ID, d.Status)
		if err != nil {
			return err
		}
		return bus.Publish(ctx, env.WithCorrelation(d.ID))
	}
}

// HTTPStatusSender posts to each consumer directly (EVENT_PUBLISHING=http,
// rollback only).
func HTTPStatusSender(targets []string, key string) StatusSender {
	client := &http.Client{Timeout: 3 * time.Second}
	return func(ctx context.Context, d repository.Delivery) error {
		payload, err := json.Marshal(d.Status)
		if err != nil {
			return err
		}
		for _, target := range targets {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target, "/")+"/internal/vendor-status", bytes.NewReader(payload))
			if err != nil {
				return err
			}
			serviceauth.SetRequestHeaders(req, key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("%s answered %d", target, resp.StatusCode)
			}
		}
		return nil
	}
}

// DispatchStatus sends queued shop status changes, retrying with backoff
// and parking after the outbox's attempt limit.
func DispatchStatus(ctx context.Context, outbox repository.Outbox, send StatusSender, log zerolog.Logger) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		batch, err := outbox.Claim(ctx)
		if err != nil {
			log.Error().Msg("vendor_outbox_claim_failed")
			continue
		}
		for _, d := range batch {
			err := send(ctx, d)
			if cerr := outbox.Complete(ctx, d, err == nil); cerr != nil {
				log.Error().Str("event_id", d.ID).Msg("vendor_outbox_complete_failed")
			}
			if err != nil {
				log.Warn().Err(err).Str("event_id", d.ID).Int("attempt", d.Attempts).Bool("parked", d.Attempts >= 10).Msg("vendor_status_propagation_pending")
			}
		}
	}
}
