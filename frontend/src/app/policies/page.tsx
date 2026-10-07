"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";

import { PageShell } from "@/components/page-shell";
import { EmptyState, LoadingState } from "@/components/states/query-state";
import { Card, CardContent } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { POLICY_KINDS, formatEffectiveAt } from "@/lib/policies";

// The marketplace policies in force: who sells, how delivery, returns and
// refunds work. Each order keeps the versions it was placed under.
export default function PoliciesPage() {
  const query = useQuery({
    queryKey: ["public-policies"],
    queryFn: api.listPublicPolicies,
    retry: false,
  });
  const byKind = new Map((query.data ?? []).map((p) => [p.kind, p]));

  return (
    <PageShell maxWidth="sm">
      <h1 className="text-2xl font-semibold tracking-tight">Chính sách</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Đơn đã đặt luôn áp dụng phiên bản chính sách tại thời điểm đặt hàng.
      </p>
      <div className="mt-6 flex flex-col gap-2">
        {query.isPending && <LoadingState rows={4} />}
        {query.error && <EmptyState title="Chính sách chưa được công bố" />}
        {query.data &&
          POLICY_KINDS.map(({ kind, label }) => {
            const p = byKind.get(kind);
            return (
              <Link key={kind} href={`/policies/${kind}`}>
                <Card className="transition-colors hover:bg-muted/40">
                  <CardContent className="text-sm">
                    <p className="font-medium">{label}</p>
                    <p className="text-muted-foreground">
                      {p
                        ? `${p.summary} · phiên bản ${p.version}, từ ${formatEffectiveAt(p.effective_at)}`
                        : "Chưa công bố"}
                    </p>
                  </CardContent>
                </Card>
              </Link>
            );
          })}
      </div>
    </PageShell>
  );
}
