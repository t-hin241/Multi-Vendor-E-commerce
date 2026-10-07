"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useParams } from "next/navigation";

import { PageShell } from "@/components/page-shell";
import { PolicyDocument } from "@/components/policies/policy-document";
import { EmptyState, LoadingState } from "@/components/states/query-state";
import * as api from "@/lib/api-client";
import {
  formatEffectiveAt,
  isPolicyKind,
  policyKindLabel,
  policyVersionHref,
} from "@/lib/policies";

// The version of one policy in force now, with every published version
// (including one scheduled to start later).
export default function PolicyPage() {
  const { kind } = useParams<{ kind: string }>();
  const valid = isPolicyKind(kind);
  const current = useQuery({
    queryKey: ["public-policy", kind],
    queryFn: () => api.getPublicPolicy(kind as api.PolicyKind),
    enabled: valid,
    retry: false,
  });
  const history = useQuery({
    queryKey: ["public-policy-history", kind],
    queryFn: () => api.getPolicyHistory(kind as api.PolicyKind),
    enabled: valid,
    retry: false,
  });

  if (!valid) {
    return (
      <PageShell maxWidth="sm">
        <EmptyState title="Không có chính sách này" />
      </PageShell>
    );
  }
  const upcoming = (history.data ?? []).filter((p) => new Date(p.effective_at) > new Date());

  return (
    <PageShell maxWidth="sm">
      {current.isPending && <LoadingState rows={6} />}
      {current.error && <EmptyState title={`${policyKindLabel(kind)} chưa được công bố`} />}
      {current.data && <PolicyDocument policy={current.data} current />}
      {upcoming.map((p) => (
        <p
          key={p.id}
          className="mt-4 rounded-md border border-warning/50 bg-warning/10 p-3 text-sm"
        >
          Phiên bản {p.version} sẽ áp dụng cho đơn đặt từ {formatEffectiveAt(p.effective_at)}.{" "}
          <Link href={policyVersionHref(p.kind, p.version)} className="text-primary underline">
            Xem trước
          </Link>
        </p>
      ))}
      {(history.data?.length ?? 0) > 1 && (
        <section className="mt-6 text-sm">
          <p className="font-medium">Các phiên bản đã công bố</p>
          <ul className="mt-2 flex flex-col gap-1">
            {history.data!.map((p) => (
              <li key={p.id}>
                <Link
                  href={policyVersionHref(p.kind, p.version)}
                  className="text-primary underline"
                >
                  Phiên bản {p.version}
                </Link>{" "}
                <span className="text-muted-foreground">
                  từ {formatEffectiveAt(p.effective_at)}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </PageShell>
  );
}
