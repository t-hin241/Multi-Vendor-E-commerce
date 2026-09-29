"use client";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import * as ops from "@/lib/vendor-operations";

export function FinanceDashboard({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const [from, setFrom] = useState(() =>
    new Date(Date.now() - 29 * 86400000).toISOString().slice(0, 10),
  );
  const [to, setTo] = useState(() => new Date().toISOString().slice(0, 10));
  const start = from ? new Date(`${from}T00:00:00+07:00`).toISOString() : "";
  const end = to
    ? new Date(new Date(`${to}T00:00:00+07:00`).getTime() + 86400000).toISOString()
    : "";
  const query = useQuery({
    queryKey: ["vendor-finance", vendorId, from, to],
    queryFn: () => callWithAuth((t) => ops.dashboard(t, vendorId, start, end)),
    enabled: Boolean(start && end && start < end),
    refetchInterval: 60000,
  });
  const data = query.data;
  function amount(label: string, value: ops.Amount | undefined) {
    return (
      <Card key={label}>
        <CardContent>
          <p className="text-sm text-muted-foreground">{label}</p>
          <p className="mt-2 text-xl font-semibold">
            {value?.available && value.value !== null ? formatMoney(value.value, "VND") : "—"}
          </p>
          {!value?.available && (
            <p className="text-xs text-muted-foreground">Chưa có số đối soát</p>
          )}
        </CardContent>
      </Card>
    );
  }
  return (
    <section className="space-y-4">
      <h2 className="text-lg font-semibold">Đơn hàng và thanh toán</h2>
      <div className="flex gap-3">
        <label>
          Từ ngày
          <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
        </label>
        <label>
          Đến ngày
          <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
        </label>
      </div>
      <p className="text-xs text-muted-foreground">
        Ngày Việt Nam · VND · Giá trị đặt hàng tính theo ngày tạo đơn; tiền đã trả tính theo ngày
        Payment ghi nhận. Giá trị đặt hàng gồm cả đơn hủy, chưa trừ hoàn tiền.
      </p>
      {(!start || !end || start >= end) && <p role="alert">Chọn khoảng ngày hợp lệ.</p>}
      {query.isPending && <p>Đang tải số liệu…</p>}
      {(query.error || data?.orders_unavailable || data?.payments_unavailable) && (
        <p role="alert" className="text-sm text-destructive">
          Không cập nhật được đầy đủ số liệu. Dữ liệu đang hiển thị có thể đã cũ; thử tải lại sau.
        </p>
      )}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {amount("Giá trị đặt hàng", data?.orders?.gross_ordered)}
        {amount("Tiền đã thu", data?.payments?.captured)}
        {amount("Tiền đã hoàn", data?.payments?.refunded)}
        {amount("Đủ điều kiện thanh toán", data?.payments?.eligible)}
        {amount("Đã trả cho cửa hàng", data?.payments?.paid_out)}
      </div>
      {data?.orders && (
        <p>
          {data.orders.total_orders} đơn trong kỳ · {data.orders.awaiting_fulfillment} đơn chờ xử
          lý.{" "}
          <Link className="underline" href="/vendor/orders">
            Xem đơn hàng
          </Link>
        </p>
      )}
      {data && (
        <p className="text-xs text-muted-foreground">
          Cập nhật lúc {new Date(data.as_of).toLocaleString("vi-VN")}
        </p>
      )}
    </section>
  );
}
