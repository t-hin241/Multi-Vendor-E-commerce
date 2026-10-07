import Link from "next/link";

import type { PolicySnapshot } from "@/lib/api-client";
import { policyKindLabel, policyVersionHref, returnRulesSummary } from "@/lib/policies";

// CheckoutPolicyNotice names the exact policy versions the order will be
// placed under (from Order's preview) with a link to each. The versions
// shown are sent back with the order; if a newer one comes into force
// meanwhile, Order refuses and the buyer reviews again.
export function CheckoutPolicyNotice({ snapshot }: { snapshot: PolicySnapshot }) {
  return (
    <div className="flex flex-col gap-1 text-xs text-muted-foreground">
      <p>{returnRulesSummary(snapshot)}</p>
      {snapshot.policies.length > 0 && (
        <p>
          Bằng việc đặt hàng, bạn đồng ý với{" "}
          {snapshot.policies.map((p, i) => (
            <span key={p.policy_id}>
              {i > 0 && ", "}
              <Link
                href={policyVersionHref(p.kind, p.version)}
                target="_blank"
                className="text-primary underline"
              >
                {policyKindLabel(p.kind)} (phiên bản {p.version})
              </Link>
            </span>
          ))}
          .
        </p>
      )}
    </div>
  );
}
