"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { StockSelfService } from "@/components/vendor/stock-self-service";
import { StockCountForm } from "@/components/vendor/stock-count-form";
import { listMyInventory } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { useConsoleShops } from "@/lib/hooks/use-console-shops";
import { can } from "@/lib/shop-access";

export default function VendorInventoryPage() {
  const { activeShop: shop } = useConsoleShops();
  const { callWithAuth } = useAuth();
  const client = useQueryClient();
  const [pagination, setPagination] = useState({ vendor: "", page: 0 });
  const page = pagination.vendor === shop?.id ? pagination.page : 0;
  const readable = can(shop, "inventory.read");
  const inventory = useQuery({
    queryKey: ["vendor-inventory-page", shop?.id, page],
    queryFn: () =>
      callWithAuth((t) => listMyInventory(t, shop!.id, { limit: 20, offset: page * 20 })),
    enabled: !!shop && readable,
  });
  function refresh() {
    client.invalidateQueries({ queryKey: ["vendor-inventory-page"] });
    client.invalidateQueries({ queryKey: ["vendor-inventory"] });
  }
  if (!shop) return <p>Chọn shop để xem tồn kho.</p>;
  if (!readable) return <p>Bạn chưa có quyền xem tồn kho của shop này.</p>;
  return (
    <section className="space-y-4">
      <h1 className="text-xl font-semibold">Tồn kho</h1>
      {inventory.isPending && <p>Đang tải…</p>}
      {inventory.error && <p role="alert">Không thể tải tồn kho.</p>}
      {inventory.data?.length === 0 && <p>Chưa có mã kho ở trang này.</p>}
      {inventory.data?.map((item) => (
        <article key={item.id} className="space-y-3 rounded border p-4">
          <p className="break-all">Mã kho: {item.id}</p>
          <p className="break-all">
            Sản phẩm: {item.product_id}
            {item.variant_id && ` · Phiên bản: ${item.variant_id}`}
          </p>
          <p>
            Khả dụng: {item.available_quantity} · Đang giữ: {item.reserved_quantity} · Thực có:{" "}
            {item.available_quantity + item.reserved_quantity}
          </p>
          <StockSelfService item={item} onChanged={refresh} />
          {can(shop, "inventory.adjust") && <StockCountForm item={item} onRecorded={refresh} />}
        </article>
      ))}
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={page === 0}
          onClick={() => setPagination({ vendor: shop.id, page: page - 1 })}
        >
          Trang trước
        </Button>
        <Button
          variant="outline"
          disabled={inventory.data?.length !== 20}
          onClick={() => setPagination({ vendor: shop.id, page: page + 1 })}
        >
          Trang sau
        </Button>
      </div>
    </section>
  );
}
