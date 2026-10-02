package eventbus

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Runtime is a service's event-bus wiring: the connection, its inbox, and
// whether its producers publish here (EVENT_PUBLISHING=jetstream) or still
// call consumers directly (http, rollback only).
type Runtime struct {
	Bus     *Bus
	Inbox   Inbox
	Publish bool
	log     zerolog.Logger
	done    chan struct{}
}

// Start connects (without waiting for the broker) and prepares the inbox.
func Start(natsURL string, cred Credentials, publishing string, pool *pgxpool.Pool, log zerolog.Logger) (*Runtime, error) {
	bus, err := Connect(natsURL, cred, log)
	if err != nil {
		return nil, err
	}
	if publishing != "jetstream" {
		log.Warn().Str("event_publishing", publishing).Msg("event_bus_publishing_disabled")
	}
	return &Runtime{Bus: bus, Inbox: Inbox{Pool: pool}, Publish: publishing == "jetstream", log: log, done: make(chan struct{})}, nil
}

// Run consumes subs until ctx ends and trims processed inbox rows once an
// hour (kept 30 days: longer than the stream's 14-day replay window).
func (r *Runtime) Run(ctx context.Context, subs ...Subscription) {
	go func() {
		defer close(r.done)
		if len(subs) > 0 {
			go r.purge(ctx)
			r.Bus.Consume(ctx, r.Inbox, subs...)
		}
	}()
}

func (r *Runtime) purge(ctx context.Context) {
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if n, err := r.Inbox.PurgeProcessed(ctx, 30*24*time.Hour); err != nil {
				r.log.Warn().Err(err).Msg("event_inbox_purge_failed")
			} else if n > 0 {
				r.log.Info().Int64("rows", n).Msg("event_inbox_purged")
			}
		}
	}
}

// RegisterAdmin adds the parked-event routes to an admin route group.
func (r *Runtime) RegisterAdmin(group gin.IRoutes, roles RoleVerifier) {
	Admin{Bus: r.Bus, Inbox: r.Inbox, Roles: roles, Log: r.log}.Register(group)
}

// Close waits for consumers to finish their current message (ctx of Run
// must be cancelled first, bounded by timeout) and drains the connection.
func (r *Runtime) Close(timeout time.Duration) {
	select {
	case <-r.done:
	case <-time.After(timeout):
		r.log.Warn().Msg("event_consumers_stop_timeout")
	}
	r.Bus.Close()
}
