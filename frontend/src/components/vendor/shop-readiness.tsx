"use client";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import type { Vendor } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { resubmit } from "@/lib/vendor-operations";
export function ShopReadiness({ vendor }: { vendor: Vendor }) {
  const { callWithAuth, setSelectedVendorId } = useAuth();
  const cache = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => callWithAuth((t) => resubmit(t, vendor.id)),
    onSuccess: () => cache.invalidateQueries({ queryKey: ["my-vendors"] }),
  });
  return (
    <div className="mt-3 space-y-2 text-sm">
      {(vendor.status === "pending" || vendor.status === "rejected") && (
        <p>
          Để được duyệt, hãy bổ sung mô tả cửa hàng và{" "}
          <Link
            className="underline"
            href="/vendor/shipping"
            onClick={() => setSelectedVendorId(vendor.id)}
          >
            địa chỉ lấy hàng mặc định
          </Link>
          .
        </p>
      )}
      {vendor.status === "suspended" && (
        <p className="text-destructive">
          Tạm khóa bán hàng: {vendor.suspension_reason}. Bạn vẫn có thể xử lý các đơn cũ trong mục
          Đơn hàng và Vận chuyển.
        </p>
      )}
      {vendor.status === "rejected" && (
        <Button size="sm" disabled={mutation.isPending} onClick={() => mutation.mutate()}>
          Nộp lại hồ sơ
        </Button>
      )}
      {mutation.error && (
        <p role="alert" className="text-destructive">
          {mutation.error.message}
        </p>
      )}
    </div>
  );
}
