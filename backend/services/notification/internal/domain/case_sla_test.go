package domain

import (
	"strings"
	"testing"
)

func TestSLANoticesRetainFullResourceReference(t *testing.T) {
	id := "00000000-0000-0000-0000-000000000001"
	for typ, path := range map[Type]string{"sla_support": "/admin/support/", "sla_return": "/admin/returns?return_id=", "sla_refund": "/admin/refunds?refund_id=", "sla_interception": "/admin/fulfillment?shipment_id="} {
		t.Run(string(typ), func(t *testing.T) {
			_, body, e := Render(&Notification{Type: typ, TemplateVersion: "v1", ReferenceID: id}, "Test Admin")
			if e != nil || !strings.Contains(body, path+id) {
				t.Fatal("notice cannot locate resource", body, e)
			}
		})
	}
}
