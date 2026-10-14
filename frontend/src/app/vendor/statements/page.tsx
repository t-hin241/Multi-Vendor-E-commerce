"use client";
import { StatementPanel } from "@/components/vendor/statement-panel";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";
import { can } from "@/lib/shop-access";
export default function StatementsPage() {
  const { activeShop, query } = useConsoleShops();
  if (query.isPending) return <p>Đang tải cửa hàng…</p>;
  if (query.isError || !activeShop || !can(activeShop, "finance.read"))
    return <p role="alert">Bạn cần quyền xem tài chính của cửa hàng.</p>;
  return <StatementPanel key={activeShop.id} vendorId={activeShop.id} />;
}
