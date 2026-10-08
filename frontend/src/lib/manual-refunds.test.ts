import { describe, expect, it } from "vitest";

import type { RefundAttempt } from "@/lib/api-client";
import {
  attemptActions,
  attemptDecisionRef,
  checkBeneficiary,
  destinationDecisionRef,
  destinationRevealRef,
} from "@/lib/manual-refunds";

function attempt(over: Partial<RefundAttempt>): RefundAttempt {
  return {
    id: "a1",
    refund_id: "r1",
    destination_version: 1,
    amount: 100,
    currency: "VND",
    stage: "ready",
    version: 1,
    prepared_by: "prep",
    prepare_reason: "x",
    evidence: [],
    created_at: "2026-10-07T00:00:00Z",
    updated_at: "2026-10-07T00:00:00Z",
    ...over,
  };
}

describe("manual refund proofs", () => {
  it("binds each proof to one operation like the Payment service", () => {
    expect(destinationDecisionRef("r1", 2, true)).toBe("refund_destination:r1:v2:verify");
    expect(destinationDecisionRef("r1", 2, false)).toBe("refund_destination:r1:v2:reject");
    expect(destinationRevealRef("r1", 3)).toBe("refund_destination:r1:v3:reveal");
    expect(attemptDecisionRef("a1", 4, true)).toBe("refund_attempt:a1:v4:confirm");
    expect(attemptDecisionRef("a1", 4, false)).toBe("refund_attempt:a1:v4:fail");
  });
});

describe("checkBeneficiary", () => {
  it("accepts a well-formed destination and refuses obvious mistakes", () => {
    expect(
      checkBeneficiary({
        bank_code: "vcb",
        account_number: "0123 4567-89",
        account_name: "Nguyễn Văn A",
      }),
    ).toBeNull();
    expect(
      checkBeneficiary({ bank_code: "v", account_number: "0123456789", account_name: "A B" }),
    ).not.toBeNull();
    expect(
      checkBeneficiary({ bank_code: "VCB", account_number: "12ab", account_name: "A B" }),
    ).not.toBeNull();
    expect(
      checkBeneficiary({ bank_code: "VCB", account_number: "0123456789", account_name: "A1" }),
    ).not.toBeNull();
  });
});

describe("attemptActions", () => {
  const now = new Date("2026-10-07T10:00:00Z");
  it("lets only the claimer submit or cancel a live claim", () => {
    const claimed = attempt({
      stage: "executing",
      claimed_by: "op",
      lease_expires_at: "2026-10-07T10:30:00Z",
    });
    expect(attemptActions(claimed, "op", now)).toMatchObject({
      submit: true,
      cancel: true,
      decide: false,
    });
    expect(attemptActions(claimed, "other", now)).toMatchObject({ submit: false, cancel: false });
    const expired = attempt({
      stage: "executing",
      claimed_by: "op",
      lease_expires_at: "2026-10-07T09:00:00Z",
    });
    expect(attemptActions(expired, "op", now).cancel).toBe(false);
  });

  it("keeps the executor from confirming their own transfer", () => {
    const submitted = attempt({ stage: "submitted", claimed_by: "op", submitted_by: "op" });
    expect(attemptActions(submitted, "op", now)).toMatchObject({ confirm: false, decide: false });
    expect(attemptActions(submitted, "reviewer", now)).toMatchObject({
      confirm: true,
      decide: true,
    });
  });

  it("lets an unknown transfer be submitted or failed, never confirmed directly", () => {
    const unknown = attempt({ stage: "unknown", claimed_by: "op" });
    expect(attemptActions(unknown, "reviewer", now)).toMatchObject({
      submit: true,
      decide: true,
      confirm: false,
    });
  });
});
