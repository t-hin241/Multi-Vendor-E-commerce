"use client";

import { useQuery } from "@tanstack/react-query";
import { useParams } from "next/navigation";

import { PageShell } from "@/components/page-shell";
import { PolicyDocument } from "@/components/policies/policy-document";
import { EmptyState, LoadingState } from "@/components/states/query-state";
import * as api from "@/lib/api-client";
import { isPolicyKind } from "@/lib/policies";

// One published version, e.g. the one an order was placed under. Published
// versions never change, so this link stays valid.
export default function PolicyVersionPage() {
  const params = useParams<{ kind: string; version: string }>();
  const version = Number(params.version);
  const valid = isPolicyKind(params.kind) && Number.isInteger(version) && version > 0;
  const query = useQuery({
    queryKey: ["public-policy-version", params.kind, version],
    queryFn: () => api.getPolicyVersion(params.kind as api.PolicyKind, version),
    enabled: valid,
    retry: false,
    staleTime: Infinity,
  });
  const current = useQuery({
    queryKey: ["public-policy", params.kind],
    queryFn: () => api.getPublicPolicy(params.kind as api.PolicyKind),
    enabled: valid,
    retry: false,
  });

  return (
    <PageShell maxWidth="sm">
      {!valid && <EmptyState title="Không có phiên bản này" />}
      {valid && query.isPending && <LoadingState rows={6} />}
      {query.error && <EmptyState title="Không tìm thấy phiên bản chính sách này" />}
      {query.data && (
        <PolicyDocument policy={query.data} current={current.data?.id === query.data.id} />
      )}
    </PageShell>
  );
}
