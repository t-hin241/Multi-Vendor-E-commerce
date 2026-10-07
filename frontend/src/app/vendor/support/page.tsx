"use client";

import Link from "next/link";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { SupportStatusBadge } from "@/components/support/support-status-badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useAuth } from "@/lib/auth-context";
import { useSupportCases } from "@/lib/hooks/use-support-cases";
import {
  SUPPORT_STATUS_OPTIONS,
  isOverdue,
  supportCategoryLabel,
  supportStatusLabel,
} from "@/lib/support-cases";

// The shop's support inbox: cases buyers opened on this shop's packages.
// The shop answers the public part; the marketplace decides the outcome.
export default function VendorSupportPage() {
  const { selectedVendorId } = useAuth();
  const [status, setStatus] = useState("");
  const cases = useSupportCases(
    "vendor",
    { vendorId: selectedVendorId ?? undefined, status: status || undefined },
    Boolean(selectedVendorId),
  );

  if (!selectedVendorId) {
    return (
      <p className="text-sm text-muted-foreground">
        Bạn chưa có cửa hàng nào.{" "}
        <Link href="/vendor/shops" className="text-primary underline">
          Quản lý cửa hàng
        </Link>
      </p>
    );
  }
  const items = cases.data?.pages.flatMap((p) => p.items) ?? [];

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        as="h1"
        title="Yêu cầu hỗ trợ"
        subtitle="Người mua báo vấn đề với gói hàng của shop. Trả lời rõ ràng, kèm mã vận đơn hoặc ảnh nếu có."
        action={
          // Radix Select reserves "" for "no selection": "all" maps back to "".
          <Select value={status || "all"} onValueChange={(v) => setStatus(v === "all" ? "" : v)}>
            <SelectTrigger className="w-52">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SUPPORT_STATUS_OPTIONS.map((s) => (
                <SelectItem key={s || "all"} value={s || "all"}>
                  {s ? supportStatusLabel(s) : "Tất cả"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        }
      />
      {cases.isPending && <LoadingState rows={3} />}
      {cases.error && (
        <ErrorState message="Không tải được yêu cầu hỗ trợ." onRetry={cases.refetch} />
      )}
      {cases.data && items.length === 0 && <EmptyState title="Không có yêu cầu nào" />}
      {items.map((c) => (
        <Link key={c.id} href={`/vendor/support/${c.id}`}>
          <Card className="transition-colors hover:bg-muted/40">
            <CardContent className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <div>
                <p className="font-medium">
                  {supportCategoryLabel(c.category)}
                  {c.status === "waiting_vendor" && (
                    <span className="ml-2 text-warning">· Sàn đang chờ shop trả lời</span>
                  )}
                </p>
                <p className="text-muted-foreground">
                  Mã #{c.id.slice(0, 8)} · gói hàng #{c.vendor_order_id.slice(0, 8)} ·{" "}
                  {new Date(c.created_at).toLocaleString("vi-VN")}
                  {isOverdue(c) && <span className="text-destructive"> · quá hạn</span>}
                </p>
              </div>
              <SupportStatusBadge status={c.status} />
            </CardContent>
          </Card>
        </Link>
      ))}
      {cases.hasNextPage && (
        <Button
          variant="outline"
          disabled={cases.isFetchingNextPage}
          onClick={() => cases.fetchNextPage()}
        >
          {cases.isFetchingNextPage ? "Đang tải…" : "Xem thêm"}
        </Button>
      )}
    </div>
  );
}
