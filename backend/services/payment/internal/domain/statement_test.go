package domain

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

func TestStatementRangeAndCursor(t *testing.T) {
	from, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00+07:00")
	f := StatementFilter{From: from, To: from.Add(24 * time.Hour), Currency: "VND"}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	c := StatementCursor{VendorID: "shop-a", Filter: f, At: from, ID: "00000000-0000-4000-8000-000000000001"}
	if _, err := ParseStatementCursor(c.Encode(), "shop-a", f); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseStatementCursor(c.Encode(), "shop-b", f); err == nil {
		t.Fatal("cross-shop cursor accepted")
	}
	other := f
	other.To = other.To.Add(time.Hour)
	if _, err := ParseStatementCursor(c.Encode(), "shop-a", other); err == nil {
		t.Fatal("changed period accepted")
	}
	for _, bad := range []StatementFilter{{From: from, To: from, Currency: "VND"}, {From: from, To: from.Add(367 * 24 * time.Hour), Currency: "VND"}, {From: from, To: f.To, Currency: "USD"}} {
		if bad.Validate() == nil {
			t.Fatal("invalid filter accepted")
		}
	}
}

func TestStatementCSVFormulaProtectionAndEmptyOpening(t *testing.T) {
	for _, s := range []string{"=SUM(1,2)", "+cmd", "-cmd", "@x", "\t=cmd", " \r@cmd"} {
		if !strings.HasPrefix(SafeCSVText(s), "'") {
			t.Errorf("unsafe cell %q", s)
		}
	}
	s := Statement{Filter: StatementFilter{Currency: "VND"}, Balances: StatementBalances{Opening: -50, Closing: -75}, AsOf: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	var b bytes.Buffer
	n := 0
	err := WriteStatementCSV(&b, s, func() (StatementEntry, bool, error) {
		n++
		return StatementEntry{ID: "id", Amount: -25, Currency: "VND", SourceRef: "=1+1"}, n == 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[1][3] != "-25" || rows[1][5] != "'=1+1" || rows[1][11] != "-50" || rows[1][14] != "-75" {
		t.Fatalf("bad CSV: %v", rows)
	}
	if err := WriteStatementCSV(&b, s, func() (StatementEntry, bool, error) { return StatementEntry{}, false, nil }); err != nil {
		t.Fatal(err)
	}
	rows, err = csv.NewReader(&b).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][2] != "opening_balance" {
		t.Fatalf("empty period lost opening: %v %v", rows, err)
	}
}
