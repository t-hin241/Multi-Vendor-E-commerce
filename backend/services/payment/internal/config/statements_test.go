package config

import "testing"

func TestStatementConfiguration(t *testing.T) {
	t.Setenv("FEATURE_VENDOR_STATEMENTS_ENABLED", "false")
	t.Setenv("STATEMENT_STORAGE_ENDPOINT", "")
	t.Setenv("EXPORT_RETENTION_HOURS", "24")
	c, err := loadStatements(false)
	if err != nil || c.Enabled || c.Retention.Hours() != 24 || c.Storage != nil {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for _, v := range []string{"0", "169", "x"} {
		t.Setenv("EXPORT_RETENTION_HOURS", v)
		if _, err := loadStatements(false); err == nil {
			t.Fatal("invalid retention accepted")
		}
	}
	t.Setenv("EXPORT_RETENTION_HOURS", "24")
	t.Setenv("STATEMENT_STORAGE_ENDPOINT", "minio:9000")
	t.Setenv("STATEMENT_STORAGE_ACCESS_KEY", "fake-af14-access")
	t.Setenv("STATEMENT_STORAGE_SECRET_KEY", "fake-af14-secret")
	t.Setenv("STATEMENT_STORAGE_DOWNLOAD_ORIGIN", "http://files.example.invalid")
	if _, err := loadStatements(true); err == nil {
		t.Fatal("insecure production download accepted")
	}
	t.Setenv("STATEMENT_STORAGE_DOWNLOAD_ORIGIN", "https://files.example.invalid")
	if _, err := loadStatements(true); err != nil {
		t.Fatal(err)
	}
}
