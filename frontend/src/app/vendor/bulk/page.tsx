"use client";
import { BulkOperations } from "@/components/vendor/bulk-operations";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";

export default function VendorBulkPage() {
  const { activeShop, query } = useConsoleShops();
  if (query.isPending) return <p>Đang tải cửa hàng…</p>;
  if (query.isError || !activeShop) return <p role="alert">Chọn cửa hàng để nhập/xuất dữ liệu.</p>;
  return <BulkOperations key={activeShop.id} shop={activeShop} />;
}
