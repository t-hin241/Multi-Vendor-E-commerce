package usecase_test

import (
	"net/url"
	"testing"
)

func TestWorkItemsUnavailableAndMissingSourcesAreNotZero(t *testing.T) {
	r := &fakeReader{ops: map[string]string{"order": `{"items":[],"generated_at":"2026-10-07T00:00:00Z"}`}, down: map[string]bool{"payment": true}}
	s := service(r, "order", "payment")
	out, e := s.WorkItems(t.Context(), "admin", "Bearer fake-admin-token", url.Values{"status": {"overdue"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Sources) != 3 {
		t.Fatal("missing source silently dropped")
	}
	for _, source := range out.Sources {
		if source.Name == "order" {
			if source.Status != "ok" || source.Page == nil {
				t.Fatal(source)
			}
		} else if source.Status != "unavailable" || source.Page != nil {
			t.Fatal("outage displayed as zero", source)
		}
	}
	for _, auth := range r.auth {
		if auth != "Bearer fake-admin-token" {
			t.Fatal("admin identity not forwarded")
		}
	}
}
