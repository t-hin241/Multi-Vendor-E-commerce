package payos

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shopee/backend/services/payment/internal/provider"
)

const fakeChecksum = "fake-checksum-key-not-a-real-secret"

// hmacOf signs the canonical string exactly as payOS documents it, without
// going through the adapter, so the test catches canonicalization bugs.
func hmacOf(canonical string) string {
	mac := hmac.New(sha256.New, []byte(fakeChecksum))
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}

// A realistic payOS webhook: large orderCode, round amount, null fields.
const webhookData = `{"orderCode":100000000000123,"amount":10000,"description":"DH0000123","accountNumber":"12345678","reference":"FT-FAKE-001","transactionDateTime":"2026-09-30 10:00:00","currency":"VND","paymentLinkId":"fake-link-1","code":"00","desc":"success","counterAccountBankId":null,"counterAccountBankName":null,"counterAccountName":null,"counterAccountNumber":null,"virtualAccountName":null,"virtualAccountNumber":null}`

const webhookCanonical = "accountNumber=12345678&amount=10000&code=00&counterAccountBankId=&counterAccountBankName=&counterAccountName=&counterAccountNumber=&currency=VND&desc=success&description=DH0000123&orderCode=100000000000123&paymentLinkId=fake-link-1&reference=FT-FAKE-001&transactionDateTime=2026-09-30 10:00:00&virtualAccountName=&virtualAccountNumber="

func TestVerifyAcceptsRealisticSignedWebhook(t *testing.T) {
	p := New("fake-client", "fake-key", fakeChecksum, "https://example.test")
	payload := []byte(`{"code":"00","desc":"success","success":true,"data":` + webhookData + `,"signature":"` + hmacOf(webhookCanonical) + `"}`)
	event, err := p.Verify(payload, "")
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if event.Type != provider.EventPaymentSucceeded || event.ProviderEventID != "txn:FT-FAKE-001" ||
		event.ProviderIntentID != "fake-link-1" || event.ProviderReference != "100000000000123" || event.Amount != 10000 || event.Currency != "VND" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestVerifyRejectsTamperedAmount(t *testing.T) {
	p := New("fake-client", "fake-key", fakeChecksum, "https://example.test")
	tampered := strings.Replace(webhookData, `"amount":10000`, `"amount":1000`, 1)
	payload := []byte(`{"code":"00","success":true,"data":` + tampered + `,"signature":"` + hmacOf(webhookCanonical) + `"}`)
	if _, err := p.Verify(payload, ""); err == nil {
		t.Fatal("Verify() accepted a tampered amount")
	}
}

func TestVerifyMapsUnsuccessfulPayment(t *testing.T) {
	p := New("fake-client", "fake-key", fakeChecksum, "https://example.test")
	data := `{"orderCode":100000000000124,"amount":5000,"paymentLinkId":"fake-link-2","code":"01","reference":""}`
	canonical := "amount=5000&code=01&orderCode=100000000000124&paymentLinkId=fake-link-2&reference="
	payload := []byte(`{"code":"00","success":false,"data":` + data + `,"signature":"` + hmacOf(canonical) + `"}`)
	event, err := p.Verify(payload, "")
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != provider.EventPaymentFailed || event.ProviderEventID != "link:fake-link-2:00:01" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestCreateIntentUsesReferenceAndShortDescription(t *testing.T) {
	deadline := time.Now().Add(10 * time.Minute)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body createRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.OrderCode != 100000000000777 || len(body.Description) > 9 || body.ExpiredAt == nil || *body.ExpiredAt != deadline.Unix() {
			t.Errorf("unexpected request %+v", body)
		}
		canonical := "amount=100&cancelUrl=https://example.test/c&description=" + body.Description + "&orderCode=100000000000777&returnUrl=https://example.test/r"
		if body.Signature != hmacOf(canonical) {
			t.Error("create signature does not follow the documented canonical form")
		}
		_, _ = w.Write([]byte(`{"code":"00","data":{"paymentLinkId":"fake-link","checkoutUrl":"https://example.test/pay"}}`))
	}))
	defer server.Close()
	p := New("fake-client", "fake-key", fakeChecksum, server.URL)
	result, err := p.CreateIntent(t.Context(), provider.CreateIntentInput{Reference: "100000000000777", OrderID: "test-order", Amount: 100, Currency: "VND",
		ExpiresAt: &deadline, ReturnURL: "https://example.test/r", CancelURL: "https://example.test/c"})
	if err != nil || result.ProviderIntentID != "fake-link" || !result.ExpiresAt.Equal(deadline) {
		t.Fatalf("unexpected %+v %v", result, err)
	}
}

func TestProviderErrorsAreClassified(t *testing.T) {
	status, body := 200, `{"code":"231","desc":"exists"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	p := New("fake-client", "fake-key", fakeChecksum, server.URL)
	in := provider.CreateIntentInput{Reference: "100000000000001", Amount: 100, Currency: "VND"}
	if _, err := p.CreateIntent(t.Context(), in); !errors.Is(err, provider.ErrRejected) {
		t.Fatalf("a payOS error code is a definite rejection, got %v", err)
	}
	status, body = 503, ``
	if _, err := p.CreateIntent(t.Context(), in); err == nil || errors.Is(err, provider.ErrRejected) {
		t.Fatalf("a 5xx has an unknown outcome, got %v", err)
	}
	status = 404
	if _, err := p.Query(t.Context(), "100000000000001"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("404 must be ErrNotFound, got %v", err)
	}
	if _, err := p.CreateIntent(t.Context(), provider.CreateIntentInput{Reference: "not-a-number", Amount: 100, Currency: "VND"}); !errors.Is(err, provider.ErrRejected) {
		t.Fatal("a non-numeric reference must be refused before any call")
	}
}

func TestQueryMapsStatuses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"00","data":{"id":"fake-link","amount":100,"amountPaid":100,"status":"PAID","transactions":[{"reference":"FT-FAKE-9","amount":100}]}}`))
	}))
	defer server.Close()
	p := New("fake-client", "fake-key", fakeChecksum, server.URL)
	info, err := p.Query(t.Context(), "100000000000001")
	if err != nil || info.Status != provider.LinkPaid || info.TransactionReference != "FT-FAKE-9" {
		t.Fatalf("unexpected %+v %v", info, err)
	}
	for status, want := range map[string]provider.LinkStatus{"PENDING": provider.LinkOpen, "PROCESSING": provider.LinkOpen, "CANCELLED": provider.LinkClosed, "EXPIRED": provider.LinkClosed, "SOMETHING_NEW": provider.LinkOpen} {
		if got := linkStatus(status, 100, 0); got != want {
			t.Errorf("%s: got %s want %s", status, got, want)
		}
	}
	if linkStatus("PAID", 100, 50) != provider.LinkOpen {
		t.Error("an underpaid link is not paid")
	}
}
