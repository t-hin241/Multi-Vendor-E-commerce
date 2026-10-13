package domain

import "testing"

func TestProfileValidation(t *testing.T) {
	for _, p := range []ProfileUpdate{{FullName: "  Buyer  ", ExpectedVersion: 1}, {FullName: "Vendor", Phone: "+84912345678", ExpectedVersion: 2}} {
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []ProfileUpdate{{FullName: "", ExpectedVersion: 1}, {FullName: "Buyer\nInjected", ExpectedVersion: 1}, {FullName: "Buyer", ExpectedVersion: 0}, {FullName: "Buyer", Phone: "+12", ExpectedVersion: 1}, {FullName: "Buyer", Phone: "1234567x", ExpectedVersion: 1}} {
		if err := p.Validate(); err == nil {
			t.Fatalf("accepted invalid profile: %+v", p)
		}
	}
}
