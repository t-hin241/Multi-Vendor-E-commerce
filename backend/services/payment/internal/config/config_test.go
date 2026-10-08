package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func fakeKey(b byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune(b)), 32)))
}

func TestManualRefundKeysAreRequiredOnlyWhenTheWorkflowIsOn(t *testing.T) {
	t.Setenv("FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED", "false")
	t.Setenv("REFUND_DESTINATION_KEY", "CHANGE_ME")
	cfg, err := loadManualRefunds()
	if err != nil || len(cfg.Keys) != 0 {
		t.Fatalf("a placeholder key with the workflow off only disables destinations: %v %v", cfg.Keys, err)
	}

	t.Setenv("FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED", "true")
	if _, err := loadManualRefunds(); err == nil {
		t.Fatal("the workflow on needs a real key")
	}

	t.Setenv("REFUND_DESTINATION_KEY", fakeKey('a'))
	t.Setenv("REFUND_DESTINATION_KEY_VERSION", "2")
	t.Setenv("REFUND_DESTINATION_PREVIOUS_KEYS", "1:"+fakeKey('b'))
	cfg, err = loadManualRefunds()
	if err != nil || cfg.KeyVersion != 2 || len(cfg.Keys) != 2 || cfg.Lease.Minutes() != 30 || cfg.Evidence != nil {
		t.Fatalf("rotated keys: %+v %v", cfg, err)
	}

	t.Setenv("REFUND_DESTINATION_PREVIOUS_KEYS", "2:"+fakeKey('b'))
	if _, err := loadManualRefunds(); err == nil {
		t.Fatal("a previous key cannot reuse the current version")
	}
	t.Setenv("REFUND_DESTINATION_PREVIOUS_KEYS", "")
	t.Setenv("MANUAL_REFUND_CLAIM_LEASE_MINUTES", "1")
	if _, err := loadManualRefunds(); err == nil {
		t.Fatal("a claim lease under 5 minutes is refused")
	}
	t.Setenv("MANUAL_REFUND_CLAIM_LEASE_MINUTES", "45")
	t.Setenv("REFUND_EVIDENCE_STORAGE_ENDPOINT", "minio:9000")
	if _, err := loadManualRefunds(); err == nil {
		t.Fatal("an evidence endpoint needs its credentials and bucket")
	}
}
