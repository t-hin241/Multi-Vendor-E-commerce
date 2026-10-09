"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { buyerDeliveryLabel, deliveryErrorMessage } from "@/lib/delivery-exceptions";

// DeliveryExceptionPanel shows the buyer what happened to a package that
// could not be delivered (AF-04) and asks before any redelivery: the buyer
// confirms the address (no extra charge) or declines. A refund is shown as
// pending until Payment confirms it.
export function DeliveryExceptionPanel({ order }: { order: api.Order }) {
  const { callWithAuth } = useAuth();
  const cases = useQuery({
    queryKey: ["order-delivery-exceptions", order.id],
    queryFn: () => callWithAuth((token) => api.listOrderDeliveryExceptions(token, order.id)),
    refetchInterval: (q) => (q.state.data?.some((d) => d.status !== "resolved") ? 30_000 : false),
  });
  const list = cases.data ?? [];
  if (list.length === 0) return null;
  const packages = order.vendor_orders ?? [];

  return (
    <Card className="mt-4">
      <CardHeader>
        <CardTitle className="text-base">Sự cố giao hàng</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        {list.map((d) => {
          const idx = packages.findIndex((vo) => vo.id === d.vendor_order_id);
          return (
            <div key={d.id} className="flex flex-col gap-1 rounded-md border p-2">
              <p className="font-medium">
                Gói hàng {idx >= 0 ? idx + 1 : ""} · {buyerDeliveryLabel(d.status)}
              </p>
              {d.carrier_outcome === "lost" && (
                <p className="text-muted-foreground">
                  Đơn vị vận chuyển xác nhận kiện hàng bị thất lạc.
                </p>
              )}
              {d.status === "redelivery_pending" && d.redelivery_address && (
                <p className="text-muted-foreground">
                  Giao lại tới: {d.redelivery_address.recipient_name},{" "}
                  {d.redelivery_address.street_address}, {d.redelivery_address.district},{" "}
                  {d.redelivery_address.province}
                </p>
              )}
              {d.status === "refund_pending" && (
                <p className="text-xs text-muted-foreground">
                  Khoản hoàn tiền được thông báo riêng khi được xác nhận.
                </p>
              )}
              {d.status === "resolved" && d.resolution === "redelivery" && (
                <p className="text-muted-foreground">Kiện hàng đã được giao lại thành công.</p>
              )}
              {d.status === "awaiting_buyer" && (
                <ConsentForm exception={d} orderId={order.id} order={order} />
              )}
            </div>
          );
        })}
      </CardContent>
    </Card>
  );
}

function ConsentForm({
  exception: d,
  orderId,
  order,
}: {
  exception: api.DeliveryException;
  orderId: string;
  order: api.Order;
}) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [addressId, setAddressId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const addresses = useQuery({
    queryKey: ["buyer-addresses"],
    queryFn: () => callWithAuth((token) => api.listBuyerAddresses(token)),
  });
  const answer = useMutation({
    mutationFn: (accept: boolean) =>
      callWithAuth((token) =>
        api.consentRedelivery(token, d.id, {
          accept,
          address_id: accept && addressId ? addressId : undefined,
          expected_version: d.version,
        }),
      ),
    onSuccess: async () => {
      setError(null);
      await queryClient.invalidateQueries({ queryKey: ["order-delivery-exceptions", orderId] });
    },
    onError: (err) => setError(deliveryErrorMessage(err, "Không gửi được xác nhận.")),
  });

  return (
    <div className="mt-1 flex flex-col gap-2 rounded-md bg-muted/40 p-2">
      <p>Sàn đề nghị giao lại kiện hàng này. Bạn không phải trả thêm phí. Chọn địa chỉ nhận:</p>
      <label className="flex items-start gap-2 text-xs">
        <input
          type="radio"
          name={`redelivery-${d.id}`}
          checked={addressId === ""}
          onChange={() => setAddressId("")}
        />
        <span>
          Địa chỉ của đơn: {order.recipient_name}, {order.street_address}, {order.district},{" "}
          {order.province}
        </span>
      </label>
      {(addresses.data ?? []).map((a) => (
        <label key={a.id} className="flex items-start gap-2 text-xs">
          <input
            type="radio"
            name={`redelivery-${d.id}`}
            checked={addressId === a.id}
            onChange={() => setAddressId(a.id)}
          />
          <span>
            {a.recipient_name}, {a.street_address}, {a.district}, {a.province}
          </span>
        </label>
      ))}
      {error && <p className="text-xs text-destructive">{error}</p>}
      <div className="flex gap-2">
        <Button size="sm" disabled={answer.isPending} onClick={() => answer.mutate(true)}>
          Đồng ý giao lại
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={answer.isPending}
          onClick={() => answer.mutate(false)}
        >
          Không nhận, tôi muốn hoàn tiền
        </Button>
      </div>
    </div>
  );
}
