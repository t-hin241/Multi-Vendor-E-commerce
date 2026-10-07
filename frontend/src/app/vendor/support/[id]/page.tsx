"use client";

import Link from "next/link";
import { useParams } from "next/navigation";

import { SectionHeader } from "@/components/section-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { SupportStatusBadge } from "@/components/support/support-status-badge";
import { SupportThread } from "@/components/support/support-thread";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  useSupportCapability,
  useSupportCase,
  useSupportCaseRefresh,
} from "@/lib/hooks/use-support-cases";
import { supportCategoryLabel, supportResolutionLabel, vendorCanReply } from "@/lib/support-cases";

// One case on the shop's package, as the shop may see it: public messages
// only, no buyer or admin identity.
export default function VendorSupportCasePage() {
  const params = useParams<{ id: string }>();
  const { user } = useAuth();
  const enabled = user?.role === "vendor";
  const caseQuery = useSupportCase("vendor", params.id, enabled);
  const capability = useSupportCapability(enabled, "vendor");
  const refresh = useSupportCaseRefresh("vendor", params.id);

  if (caseQuery.isPending) return <LoadingState rows={4} />;
  if (caseQuery.error || !caseQuery.data) {
    return caseQuery.error instanceof api.ApiError && caseQuery.error.status === 404 ? (
      <EmptyState title="Không tìm thấy yêu cầu hỗ trợ" />
    ) : (
      <ErrorState message="Không tải được yêu cầu hỗ trợ." onRetry={caseQuery.refetch} />
    );
  }
  const c = caseQuery.data;

  return (
    <div className="flex max-w-3xl flex-col gap-4">
      <SectionHeader
        as="h1"
        title={supportCategoryLabel(c.category)}
        badge={<SupportStatusBadge status={c.status} />}
        subtitle={
          <>
            Mã #{c.id.slice(0, 8)} · gói hàng #{c.vendor_order_id.slice(0, 8)} ·{" "}
            <Link href="/vendor/orders" className="text-primary underline">
              đơn hàng của shop
            </Link>
          </>
        }
      />
      {c.status === "waiting_vendor" && (
        <p className="rounded-md border border-warning/50 bg-warning/10 p-3 text-sm">
          Sàn đang chờ shop phản hồi
          {c.due_at && ` trước ${new Date(c.due_at).toLocaleString("vi-VN")}`}.
        </p>
      )}
      {c.financial_hold && c.status !== "closed" && (
        <p className="text-sm text-muted-foreground">
          Khoản thanh toán cho gói hàng này tạm giữ đến khi yêu cầu được đóng.
        </p>
      )}
      {c.resolution_kind && (
        <p className="text-sm">
          Kết luận của sàn:{" "}
          <span className="font-medium">{supportResolutionLabel(c.resolution_kind)}</span>
          {c.resolution_note && ` — ${c.resolution_note}`}
        </p>
      )}
      <SupportThread
        scope="vendor"
        detail={c}
        canReply={vendorCanReply(c)}
        capability={capability.data}
        onChanged={refresh}
      />
    </div>
  );
}
