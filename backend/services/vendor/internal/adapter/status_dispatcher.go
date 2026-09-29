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

func DispatchStatus(ctx context.Context, outbox repository.Outbox, targets []string, key string, log zerolog.Logger) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	client := &http.Client{Timeout: 3 * time.Second}
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
			payload, err := json.Marshal(d.Status)
			if err != nil {
				log.Error().Msg("vendor_outbox_encode_failed")
				continue
			}
			ok := true
			for _, target := range targets {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(target, "/")+"/internal/vendor-status", bytes.NewReader(payload))
				if err != nil {
					ok = false
					continue
				}
				req.Header.Set(serviceauth.Header, key)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Request-ID", d.ID)
				resp, err := client.Do(req)
				if err != nil {
					ok = false
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					ok = false
				}
			}
			if err = outbox.Complete(ctx, d, ok); err != nil {
				log.Error().Str("event_id", d.ID).Msg("vendor_outbox_complete_failed")
			}
			if !ok {
				log.Warn().Str("event_id", d.ID).Int("attempt", d.Attempts).Bool("parked", d.Attempts >= 10).Msg("vendor_status_propagation_pending")
			}
		}
	}
}
