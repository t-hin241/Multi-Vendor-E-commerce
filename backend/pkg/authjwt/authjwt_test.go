package authjwt_test

import (
	"testing"
	"time"

	"shopee/backend/pkg/authjwt"
)

func TestIssueAndParse_RoundTrips(t *testing.T) {
	manager := authjwt.NewManager("test-secret")

	token, expiresAt, err := manager.IssueAccessToken("user-1", "buyer", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}
	if expiresAt.Before(time.Now()) {
		t.Fatal("expected expiry to be in the future")
	}

	claims, err := manager.Parse(token)
	if err != nil {
		t.Fatalf("unexpected error parsing token: %v", err)
	}
	if claims.UserID != "user-1" {
		t.Errorf("expected user id user-1, got %q", claims.UserID)
	}
	if claims.Role != "buyer" {
		t.Errorf("expected role buyer, got %q", claims.Role)
	}
}

func TestParse_RejectsExpiredToken(t *testing.T) {
	manager := authjwt.NewManager("test-secret")

	token, _, err := manager.IssueAccessToken("user-1", "buyer", -time.Minute)
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}

	if _, err := manager.Parse(token); err == nil {
		t.Fatal("expected an error parsing an expired token, got nil")
	}
}

func TestParse_RejectsTokenSignedWithDifferentSecret(t *testing.T) {
	issuer := authjwt.NewManager("secret-a")
	verifier := authjwt.NewManager("secret-b")

	token, _, err := issuer.IssueAccessToken("user-1", "buyer", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error issuing token: %v", err)
	}

	if _, err := verifier.Parse(token); err == nil {
		t.Fatal("expected an error parsing a token signed with a different secret, got nil")
	}
}

func TestParse_RejectsGarbage(t *testing.T) {
	manager := authjwt.NewManager("test-secret")

	if _, err := manager.Parse("not-a-jwt"); err == nil {
		t.Fatal("expected an error parsing garbage input, got nil")
	}
}
