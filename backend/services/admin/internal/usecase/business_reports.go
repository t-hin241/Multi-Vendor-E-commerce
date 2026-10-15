package usecase

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"shopee/backend/pkg/bizreport"
)

// reportSources are the owners of the AF-20 figures. Each answers with its
// own as_of and definition version; Admin only puts them side by side.
var reportSources = []call{
	{source: "order", path: "/api/orders/admin/business-reports"},
	{source: "payment", path: "/api/payments/admin/business-reports"},
	{source: "shipment", path: "/api/shipments/admin/business-reports"},
}

// BusinessReport is the marketplace (or one shop's) business report as the
// owners answered it. A source that did not answer, refused, or answered
// with another definition version is null with its reason in Sources:
// never zero, and never two definition versions side by side unlabeled.
type BusinessReport struct {
	GeneratedAt       time.Time       `json:"generated_at"`
	DefinitionVersion string          `json:"definition_version"`
	Range             bizreport.Range `json:"range"`
	Sources           []SourceStatus  `json:"sources"`
	Order             json.RawMessage `json:"order"`
	Payment           json.RawMessage `json:"payment"`
	Shipment          json.RawMessage `json:"shipment"`
}

// BusinessReport reads the owners' admin reports in parallel with the
// admin's own token (each owner checks its own bundle again).
func (s Service) BusinessReport(ctx context.Context, adminID, authorization string, r bizreport.Range) (*BusinessReport, error) {
	if !s.BusinessReports {
		return nil, bizreport.Disabled()
	}
	if err := s.requireAdmin(ctx, adminID); err != nil {
		return nil, err
	}
	q := url.Values{"from": {r.From.Format(time.RFC3339)}, "to": {r.To.Format(time.RFC3339)}, "group": {r.Group}}
	if r.VendorID != "" {
		q.Set("vendor_id", r.VendorID)
	}
	calls := make([]call, 0, len(reportSources))
	for _, c := range reportSources {
		c.query = q
		calls = append(calls, c)
	}
	results := s.fetch(ctx, authorization, calls)
	out := &BusinessReport{GeneratedAt: s.now(), DefinitionVersion: bizreport.DefinitionVersion, Range: r, Sources: []SourceStatus{}}
	for _, c := range reportSources {
		res, ok := results[c.source]
		if !ok {
			continue // owner not configured
		}
		if res.status.Status == statusOK {
			var head struct {
				DefinitionVersion string    `json:"definition_version"`
				AsOf              time.Time `json:"as_of"`
			}
			switch err := json.Unmarshal(res.data, &head); {
			case err != nil:
				res.status.Status, res.status.Error, res.status.FetchedAt, res.data = statusUnavailable, "unreadable report", nil, nil
			case head.DefinitionVersion != bizreport.DefinitionVersion:
				res.status.Status, res.status.Error, res.data = statusUnavailable, "definition version "+head.DefinitionVersion, nil
			default:
				asOf := head.AsOf
				res.status.GeneratedAt = &asOf
			}
		}
		out.Sources = append(out.Sources, res.status)
		switch c.source {
		case "order":
			out.Order = res.data
		case "payment":
			out.Payment = res.data
		case "shipment":
			out.Shipment = res.data
		}
	}
	return out, nil
}
