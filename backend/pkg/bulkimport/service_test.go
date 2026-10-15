package bulkimport

import (
	"testing"
)

func TestDuplicateTargetsFailEveryOccurrence(t *testing.T) {
	records := []Record{{RowNo: 1}, {RowNo: 2}, {RowNo: 4}, {RowNo: 5}}
	plans := []Plan{{TargetKey: "variant:a"}, {TargetKey: "variant:b"}, {TargetKey: "variant:a"}, {TargetKey: "variant:c", Error: &RowError{Code: "x"}}}
	markDuplicates(records, plans)
	if plans[0].Error == nil || plans[2].Error == nil || plans[0].Error.Code != "duplicate_target" || plans[1].Error != nil {
		t.Fatalf("%+v", plans)
	}
	if plans[0].Error.Message != "The same product/SKU appears in rows 1, 4; keep one row per target" || plans[3].Error.Code != "x" {
		t.Fatal(plans[0].Error.Message)
	}
}

func TestPreviewHashPinsWhatWasShown(t *testing.T) {
	v1, v2 := int64(1), int64(2)
	rows := []plannedRow{{RowNo: 2, Status: RowValid, TargetKey: "a", PayloadHash: "h", ExpectedVersion: &v1}, {RowNo: 1, Status: RowInvalid}}
	same := []plannedRow{rows[1], rows[0]}
	if previewHash(rows) != previewHash(same) {
		t.Fatal("hash depends on order")
	}
	changed := []plannedRow{{RowNo: 2, Status: RowValid, TargetKey: "a", PayloadHash: "h", ExpectedVersion: &v2}, rows[1]}
	if previewHash(rows) == previewHash(changed) {
		t.Fatal("version change not detected")
	}
}

func TestOutcomeCountsEveryRow(t *testing.T) {
	cases := []struct {
		status string
		s      Summary
		want   string
	}{
		{StatusApplying, Summary{Succeeded: 1}, ""},
		{StatusCompleted, Summary{Succeeded: 2, Unchanged: 3}, "succeeded"},
		{StatusCompleted, Summary{Succeeded: 1, NeedsApproval: 1, Failed: 1}, "partial"},
		{StatusCompleted, Summary{Invalid: 1, Succeeded: 1}, "partial"},
		{StatusCancelled, Summary{Cancelled: 4}, "failed"},
		{StatusCompleted, Summary{NeedsApproval: 2}, "succeeded"},
	}
	for _, c := range cases {
		if got := outcome(&Job{Status: c.status, Summary: c.s, ConfirmedBy: "someone"}); got != c.want {
			t.Errorf("%s %+v: %q", c.status, c.s, got)
		}
	}
	if got := outcome(&Job{Status: StatusCancelled, Summary: Summary{Cancelled: 2}}); got != "" {
		t.Errorf("cancelled before confirm: %q", got)
	}
}

func TestCleanFileName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\a\Bảng giá.csv`:      "Bảng giá.csv",
		"../../etc/passwd":             "passwd",
		"<script>.csv":                 "script.csv",
		"":                             "import.csv",
		"\"quoted\";rm -rf.csv":        "quotedrm -rf.csv",
		"tên file (1)_v2-final.CSV":    "tên file (1)_v2-final.CSV",
		"line\nbreak\r\x00control.csv": "linebreakcontrol.csv",
	} {
		if got := cleanFileName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestLoadConfigLimitsAndStorage(t *testing.T) {
	t.Setenv("FEATURE_BULK_OPERATIONS_ENABLED", "true")
	t.Setenv("IMPORT_MAX_ROWS", "")
	t.Setenv("IMPORT_MAX_BYTES", "")
	t.Setenv("IMPORT_STORAGE_ENDPOINT", "")
	c, err := LoadConfig("catalog-imports", false)
	if err != nil || !c.Enabled || c.MaxRows != 1000 || c.MaxBytes != 5242880 || c.Storage != nil {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err = LoadConfig("catalog-imports", true); err == nil {
		t.Fatal("production without storage accepted")
	}
	for k, v := range map[string]string{"IMPORT_MAX_ROWS": "1001", "IMPORT_MAX_BYTES": "6000000"} {
		t.Setenv(k, v)
		if _, err = LoadConfig("x", false); err == nil {
			t.Fatalf("%s=%s accepted", k, v)
		}
		t.Setenv(k, "")
	}
	t.Setenv("IMPORT_STORAGE_ENDPOINT", "minio:9000")
	t.Setenv("IMPORT_STORAGE_ACCESS_KEY", "fake-access")
	t.Setenv("IMPORT_STORAGE_SECRET_KEY", "fake-secret-not-real")
	t.Setenv("IMPORT_STORAGE_DOWNLOAD_ORIGIN", "http://localhost:9000")
	if c, err = LoadConfig("inventory-imports", false); err != nil || c.Storage == nil || c.Storage.Bucket != "inventory-imports" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err = LoadConfig("inventory-imports", true); err == nil {
		t.Fatal("plain HTTP download origin accepted in production")
	}
}
