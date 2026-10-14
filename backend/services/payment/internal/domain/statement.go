package domain

import (
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"shopee/backend/pkg/apperror"
)

const StatementMaxRows = 100000

type StatementFilter struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Currency string    `json:"currency"`
}

func StatementInvalid(message string) error {
	return &apperror.Error{Code: "statement_invalid", Message: message, Status: http.StatusUnprocessableEntity}
}

func (f StatementFilter) Validate() error {
	if f.Currency != "VND" {
		return StatementInvalid("currency must be VND")
	}
	if f.From.IsZero() || !f.To.After(f.From) || f.To.Sub(f.From) > 366*24*time.Hour {
		return StatementInvalid("from/to must be ISO timestamps defining at most 366 days; to is exclusive")
	}
	return nil
}

type StatementCursor struct {
	VendorID string          `json:"v"`
	Filter   StatementFilter `json:"f"`
	At       time.Time       `json:"at"`
	ID       string          `json:"id"`
}

func (c StatementCursor) Encode() string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func ParseStatementCursor(raw, vendor string, f StatementFilter) (*StatementCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 1024 {
		return nil, StatementInvalid("cursor is invalid")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c StatementCursor
	if err != nil || json.Unmarshal(b, &c) != nil || c.VendorID != vendor || !c.Filter.From.Equal(f.From) || !c.Filter.To.Equal(f.To) || c.Filter.Currency != f.Currency || c.At.IsZero() {
		return nil, StatementInvalid("cursor does not match this shop and period")
	}
	if _, err := uuid.Parse(c.ID); err != nil {
		return nil, StatementInvalid("cursor is invalid")
	}
	return &c, nil
}

type StatementBalances struct {
	Opening     int64 `json:"opening"`
	Credit      int64 `json:"credit"`
	Debit       int64 `json:"debit"`
	Closing     int64 `json:"closing"`
	Held        int64 `json:"held"`
	NotEligible int64 `json:"not_eligible"`
	Payable     int64 `json:"payable"`
	InPayout    int64 `json:"in_payout"`
}

// StatementEntry deliberately excludes internal notes, actors and beneficiary data.
type StatementEntry struct {
	ID                string    `json:"id"`
	VendorOrderID     *string   `json:"vendor_order_id,omitempty"`
	Type              EntryType `json:"type"`
	Amount            int64     `json:"amount"`
	Currency          string    `json:"currency"`
	SourceRef         string    `json:"source_ref"`
	RecordedAt        time.Time `json:"recorded_at"`
	OccurredAt        time.Time `json:"occurred_at"`
	OccurredEstimated bool      `json:"occurred_at_estimated"`
	EligibleAt        time.Time `json:"eligible_at"`
}

type Statement struct {
	VendorID          string            `json:"vendor_id"`
	Filter            StatementFilter   `json:"filters"`
	Balances          StatementBalances `json:"balances"`
	BalancesAvailable bool              `json:"balances_available"`
	GapCount          int64             `json:"gap_count"`
	Entries           []StatementEntry  `json:"entries"`
	NextCursor        string            `json:"next_cursor,omitempty"`
	AsOf              time.Time         `json:"as_of"`
	Watermark         *time.Time        `json:"watermark"`
}

type StatementExport struct {
	ID             string    `json:"operation_id"`
	VendorID       string    `json:"vendor_id"`
	RequesterID    string    `json:"-"`
	IdempotencyKey string    `json:"-"`
	Status         string    `json:"status"`
	StatusURL      string    `json:"status_url"`
	Snapshot       Statement `json:"snapshot"`
	ObjectKey      string    `json:"-"`
	Checksum       string    `json:"checksum,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	Attempts       int       `json:"-"`
}

type StatementPayout struct {
	ID                string   `json:"id"`
	BatchID           string   `json:"batch_id"`
	Amount            int64    `json:"amount"`
	Currency          string   `json:"currency"`
	Status            string   `json:"status"`
	MaskedDestination string   `json:"masked_destination"`
	EntryRefs         []string `json:"entry_refs"`
}

// SafeCSVText neutralizes spreadsheet formula prefixes, including after whitespace.
func SafeCSVText(s string) string {
	t := strings.TrimLeft(s, " \t\r\n")
	if t != "" && strings.ContainsRune("=+-@", rune(t[0])) {
		return "'" + s
	}
	return s
}

// WriteStatementCSV streams one immutable snapshot. Amounts remain numeric.
func WriteStatementCSV(w io.Writer, s Statement, next func() (StatementEntry, bool, error)) error {
	c := csv.NewWriter(w)
	if err := c.Write([]string{"id", "vendor_order_id", "type", "amount", "currency", "source_ref", "recorded_at", "occurred_at", "occurred_at_estimated", "eligible_at", "as_of", "opening", "credit", "debit", "closing", "held", "not_eligible", "payable", "in_payout", "gap_count"}); err != nil {
		return err
	}
	metadata := []string{s.AsOf.Format(time.RFC3339Nano), strconv.FormatInt(s.Balances.Opening, 10), strconv.FormatInt(s.Balances.Credit, 10), strconv.FormatInt(s.Balances.Debit, 10), strconv.FormatInt(s.Balances.Closing, 10), strconv.FormatInt(s.Balances.Held, 10), strconv.FormatInt(s.Balances.NotEligible, 10), strconv.FormatInt(s.Balances.Payable, 10), strconv.FormatInt(s.Balances.InPayout, 10), strconv.FormatInt(s.GapCount, 10)}
	written := false
	for {
		e, ok, err := next()
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		vo := ""
		if e.VendorOrderID != nil {
			vo = *e.VendorOrderID
		}
		row := []string{SafeCSVText(e.ID), SafeCSVText(vo), SafeCSVText(string(e.Type)), strconv.FormatInt(e.Amount, 10), e.Currency, SafeCSVText(e.SourceRef), e.RecordedAt.Format(time.RFC3339Nano), e.OccurredAt.Format(time.RFC3339Nano), strconv.FormatBool(e.OccurredEstimated), e.EligibleAt.Format(time.RFC3339Nano)}
		if err := c.Write(append(row, metadata...)); err != nil {
			return err
		}
		written = true
	}
	if !written {
		if err := c.Write(append([]string{"", "", "opening_balance", "0", s.Filter.Currency, "", "", "", "", ""}, metadata...)); err != nil {
			return err
		}
	}
	c.Flush()
	return c.Error()
}
