package bulkimport

import (
	"errors"
	"strings"
	"testing"

	"shopee/backend/pkg/apperror"
)

var testTemplate = Template{Kind: "prices", Version: 1, DynamicPrefix: "attr.", Columns: []Column{
	{Name: "sku"}, {Name: "price_amount", Required: true}, {Name: "product_name", ReadOnly: true},
}}

func parseCode(t *testing.T, data string, rows int) (*Parsed, string) {
	t.Helper()
	p, err := Parse(strings.NewReader(data), testTemplate, rows, 1<<20)
	if err == nil {
		return p, ""
	}
	var app *apperror.Error
	if !errors.As(err, &app) {
		t.Fatalf("unexpected error type %v", err)
	}
	return nil, string(app.Code)
}

func TestParseBOMUnicodeAndBlankRows(t *testing.T) {
	p, code := parseCode(t, "\xEF\xBB\xBFSKU, Price_Amount ,attr.mau_sac\r\nÁO-ĐỎ-L,199000,Đỏ tươi\r\n,,\r\n\"Q,1\",5,\"dòng\nhai\"\n", 10)
	if code != "" {
		t.Fatal(code)
	}
	if strings.Join(p.Header, "|") != "sku|price_amount|attr.mau_sac" || len(p.Records) != 2 {
		t.Fatalf("header %v records %d", p.Header, len(p.Records))
	}
	if p.Records[0].Values["sku"] != "ÁO-ĐỎ-L" || p.Records[1].RowNo != 3 || p.Records[1].Values["attr.mau_sac"] != "dòng\nhai" {
		t.Fatalf("records %+v", p.Records)
	}
	if got := p.Raw[3]; got[0] != "Q,1" {
		t.Fatalf("raw %v", got)
	}
}

func TestParseRefusesWholeFile(t *testing.T) {
	for name, data := range map[string]string{
		"empty":            "",
		"header only":      "sku,price_amount\n",
		"latin1":           "sku,price_amount\n\xC1O,1\n",
		"nul":              "sku,price_amount\nA\x00,1\n",
		"unknown column":   "sku,price_amount,secret\nA,1,x\n",
		"bad dynamic":      "sku,price_amount,attr.Bad Code\nA,1,x\n",
		"duplicate header": "sku,price_amount,SKU\nA,1,B\n",
		"missing required": "sku\nA\n",
		"ragged row":       "sku,price_amount\nA,1,extra\n",
		"bare quote":       "sku,price_amount\nA\"B,1\n",
		"too many rows":    "sku,price_amount\nA,1\nB,2\nC,3\n",
	} {
		if _, code := parseCode(t, data, 2); code != "file_invalid" {
			t.Errorf("%s: got %q", name, code)
		}
	}
	if _, err := Parse(strings.NewReader("sku,price_amount\n"+strings.Repeat("A,1\n", 100)), testTemplate, 1000, 64); err == nil {
		t.Fatal("byte limit not enforced")
	}
}

func TestCheckCellsRejectsFormulasMarkupAndLinks(t *testing.T) {
	bad := []string{"=1+1", "+1", "-1", "@SUM(A1)", "<b>x</b>", "<img src=x>", "see https://evil.test", "javascript:alert(1)", "www.example.test", "a\x07b"}
	for _, v := range bad {
		if e := CheckCells(Record{Values: map[string]string{"sku": v}}, testTemplate); e == nil || e.Code != "unsafe_value" || e.Field != "sku" {
			t.Errorf("%q accepted: %+v", v, e)
		}
	}
	good := []string{"Áo thun 3 < 5", "giá 1.000", "metadata: none", "dòng 1\ndòng 2", "100%"}
	for _, v := range good {
		if e := CheckCells(Record{Values: map[string]string{"sku": v}}, testTemplate); e != nil {
			t.Errorf("%q refused: %+v", v, e)
		}
	}
	// Read-only columns are ignored on import.
	if e := CheckCells(Record{Values: map[string]string{"product_name": "=cmd"}}, testTemplate); e != nil {
		t.Fatal(e)
	}
}

func TestCSVWriterNeutralizesFormulas(t *testing.T) {
	var b strings.Builder
	w, err := NewCSVWriter(&b)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Write([]string{"=HYPERLINK(\"x\")", "-5", "@a", "safe", "\tx"}); err != nil {
		t.Fatal(err)
	}
	if err = w.Flush(); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimPrefix(b.String(), "\xEF\xBB\xBF")
	if got != "\"'=HYPERLINK(\"\"x\"\")\",'-5,'@a,safe,'\tx\n" {
		t.Fatalf("got %q", got)
	}
}
