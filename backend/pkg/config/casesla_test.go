package config

import (
	"testing"
	"time"
)

func TestCaseSLAShadowDefaultsAndRecipientGuard(t *testing.T) {
	for _, name := range []string{"FEATURE_CASE_SLA_ENABLED", "SLA_POLL_INTERVAL", "SLA_ON_CALL_ADMIN_IDS", "SLA_ESCALATION_ADMIN_IDS"} {
		t.Setenv(name, "")
	}
	c, e := LoadCaseSLA()
	if e != nil || c.Enabled || c.PollInterval != time.Minute {
		t.Fatal(c, e)
	}
	t.Setenv("FEATURE_CASE_SLA_ENABLED", "true")
	if _, e = LoadCaseSLA(); e == nil {
		t.Fatal("enabled without roster")
	}
	t.Setenv("SLA_ON_CALL_ADMIN_IDS", "00000000-0000-0000-0000-000000000001")
	t.Setenv("SLA_ESCALATION_ADMIN_IDS", "00000000-0000-0000-0000-000000000002")
	if _, e = LoadCaseSLA(); e != nil {
		t.Fatal(e)
	}
	t.Setenv("SLA_POLL_INTERVAL", "0s")
	if _, e = LoadCaseSLA(); e == nil {
		t.Fatal("invalid interval")
	}
}
