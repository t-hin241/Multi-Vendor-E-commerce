"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

const STATUS_LABEL: Record<api.ShopPolicy["status"], string> = {
  proposed: "Chờ sàn duyệt",
  approved: "Đã duyệt, đang hiển thị",
  rejected: "Bị từ chối",
};

// ShopPolicyProposals lets a shop propose its own policy, an addition to
// the marketplace policies. It is public only after an admin approves it
// and may never take away a buyer protection of the marketplace.
export function ShopPolicyProposals({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const versions = useQuery({
    queryKey: ["shop-policies", vendorId],
    queryFn: () => callWithAuth((t) => api.listMyShopPolicies(t, vendorId)),
  });
  const items = versions.data ?? [];
  const waiting = items.some((p) => p.status === "proposed");
  const approved = items.find((p) => p.status === "approved");

  async function propose() {
    setBusy(true);
    setError(null);
    try {
      await callWithAuth((t) => api.proposeShopPolicy(t, vendorId, content.trim()));
      setContent("");
      await queryClient.invalidateQueries({ queryKey: ["shop-policies", vendorId] });
    } catch (err) {
      setError(describeApiError(err, "Không gửi được chính sách."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-3 flex flex-col gap-2 border-t pt-3 text-sm">
      <p className="font-medium">Chính sách bổ sung của shop</p>
      <p className="text-xs text-muted-foreground">
        Chỉ được bổ sung (ví dụ: đóng gói, đổi size, giờ giao). Không được bớt quyền đổi trả, hoàn
        tiền mà sàn đảm bảo. Nội dung chỉ hiển thị sau khi sàn duyệt; đơn đã đặt giữ phiên bản cũ.
      </p>
      {approved && (
        <p className="whitespace-pre-wrap rounded-md border bg-muted/30 p-2">
          <span className="block text-xs text-muted-foreground">
            Phiên bản {approved.version} đang hiển thị
          </span>
          {approved.content}
        </p>
      )}
      {items
        .filter((p) => p.status !== "approved" || p.id !== approved?.id)
        .slice(0, 3)
        .map((p) => (
          <p key={p.id} className="text-xs text-muted-foreground">
            Phiên bản {p.version}: {STATUS_LABEL[p.status]}
            {p.source === "legacy" && " (chuyển từ nội dung cũ)"}
            {p.decision_reason && ` — ${p.decision_reason}`}
          </p>
        ))}
      {!waiting && (
        <>
          <Textarea
            rows={3}
            maxLength={10000}
            value={content}
            placeholder="Nội dung đề xuất (văn bản thuần)"
            onChange={(e) => setContent(e.target.value)}
          />
          <Button
            size="sm"
            className="self-start"
            disabled={busy || !content.trim()}
            onClick={propose}
          >
            {busy ? "Đang gửi…" : "Gửi sàn duyệt"}
          </Button>
        </>
      )}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}
