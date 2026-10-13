package domain

import "testing"

func TestSelectionIsPartOfCheckoutIdentity(t *testing.T) {
	version, total := int64(3), int64(2000)
	base := CheckoutRequest{AddressID: "address", CartVersion: &version, ExpectedTotal: &total}
	legacy := base.Hash()
	base.SelectedLineIDs = []string{"line-b", "line-a"}
	selected := base.Hash()
	base.SelectedLineIDs = []string{"line-a", "line-b"}
	if base.Hash() != selected || selected == legacy {
		t.Fatal("selection hash not canonical or indistinguishable from legacy")
	}
	base.SelectedLineIDs = []string{"line-a"}
	if base.Hash() == selected {
		t.Fatal("different selection must conflict")
	}
	base.SelectedLineIDs = []string{"line-b", "line-a"}
	base.AddressID = "new-address"
	if base.Hash() == selected {
		t.Fatal("different address must conflict")
	}
}
