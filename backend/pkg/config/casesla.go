package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type CaseSLA struct {
	Enabled                  bool
	PollInterval             time.Duration
	OnCallIDs, EscalationIDs []string
}

// Disabled means shadow mode: keep deadlines and measurements, send nothing.
func LoadCaseSLA() (CaseSLA, error) {
	c := CaseSLA{PollInterval: time.Minute}
	if raw := os.Getenv("FEATURE_CASE_SLA_ENABLED"); raw != "" {
		v, e := strconv.ParseBool(raw)
		if e != nil {
			return c, fmt.Errorf("FEATURE_CASE_SLA_ENABLED must be boolean")
		}
		c.Enabled = v
	}
	if raw := os.Getenv("SLA_POLL_INTERVAL"); raw != "" {
		v, e := time.ParseDuration(raw)
		if e != nil || v < time.Second || v > time.Hour {
			return c, fmt.Errorf("SLA_POLL_INTERVAL must be between 1s and 1h")
		}
		c.PollInterval = v
	}
	for name, target := range map[string]*[]string{"SLA_ON_CALL_ADMIN_IDS": &c.OnCallIDs, "SLA_ESCALATION_ADMIN_IDS": &c.EscalationIDs} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			for _, id := range strings.Split(raw, ",") {
				id = strings.TrimSpace(id)
				if _, e := uuid.Parse(id); e != nil {
					return c, fmt.Errorf("%s must contain admin UUIDs", name)
				}
				*target = append(*target, id)
			}
		}
	}
	if c.Enabled && (len(c.OnCallIDs) == 0 || len(c.EscalationIDs) == 0) {
		return c, fmt.Errorf("case SLA delivery requires on-call and escalation admin IDs")
	}
	return c, nil
}
