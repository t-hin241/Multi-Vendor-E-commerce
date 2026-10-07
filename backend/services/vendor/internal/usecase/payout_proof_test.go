package usecase_test

import (
	"context"
	"testing"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

type recordedProofs struct{ purpose, ref string }

func (r *recordedProofs) ConsumeProof(_ context.Context, proof, _, purpose, ref string) error {
	r.purpose, r.ref = purpose, ref
	if proof != "fake-valid-proof" {
		return adminaccess.ReauthRequired()
	}
	return nil
}

// AF-19: with scoped permissions on, verifying a payout destination and
// reading its details need a password proof for that exact account version.
func TestPayoutDecisionAndDetailsNeedAProof(t *testing.T) {
	proofs := &recordedProofs{}
	uc := &usecase.PayoutUseCase{Ops: testOps(), Proofs: proofs, RequireProof: true}
	if _, err := uc.DecideWithProof(t.Context(), "admin", "shop", "acct-1", 3, true, "Bank letter", "replayed"); code(err) != adminaccess.CodeReauthRequired {
		t.Fatalf("decision without a valid proof: %v", err)
	}
	if proofs.purpose != usecase.ProofPurposePayoutDecide || proofs.ref != "payout_account:acct-1:v3:verify" {
		t.Fatalf("proof bound to the wrong operation: %+v", proofs)
	}
	if _, err := uc.DetailsWithProof(t.Context(), "admin", "shop", "acct-1", 3, "Transfer check", ""); code(err) != adminaccess.CodeReauthRequired {
		t.Fatalf("details without a proof: %v", err)
	}
	if proofs.purpose != usecase.ProofPurposePayoutDetails || proofs.ref != usecase.PayoutDetailsRef("acct-1", 3) {
		t.Fatalf("details proof bound to the wrong operation: %+v", proofs)
	}
	if usecase.PayoutDecisionRef("acct-1", 3, false) != "payout_account:acct-1:v3:reject" {
		t.Fatal("reject reference wrong")
	}
}
