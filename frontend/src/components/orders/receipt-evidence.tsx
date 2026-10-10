"use client";

import { useQuery } from "@tanstack/react-query";

import { AttachmentImage } from "@/components/support/attachment-image";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// ReceiptEvidence shows the evidence images of a return (receipt, lost
// parcel) or of a failed delivery's receipt (PW-038) to the shop or an
// admin. Nothing renders while there is none.
export function ReceiptEvidence({
  scope,
  kind,
  refId,
}: {
  scope: "vendor" | "admin";
  kind: api.ReceiptEvidenceKind;
  refId: string;
}) {
  const { callWithAuth } = useAuth();
  const list = useQuery({
    queryKey: ["receipt-evidence", scope, kind, refId],
    queryFn: () => callWithAuth((token) => api.listReceiptEvidence(token, scope, kind, refId)),
    staleTime: 60_000,
    retry: false,
  });
  if (!list.data?.length) return null;
  return (
    <div className="flex flex-wrap gap-2">
      {list.data.map((a) => (
        <AttachmentImage
          key={a.id}
          scope={scope}
          caseId={refId}
          attachment={a}
          load={(token) => api.fetchReceiptEvidence(token, scope, kind, refId, a.id)}
        />
      ))}
    </div>
  );
}
