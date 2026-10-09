"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// ReturnDestinationCard: the owner chooses which address receives returned
// goods and when (AF-05). A pickup address is not a return address by
// default; the marketplace verifies the choice before buyers are sent
// there, and any change needs a new verification.
export function ReturnDestinationCard({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const destination = useQuery({
    queryKey: ["return-destination", vendorId],
    queryFn: () => callWithAuth((token) => api.getReturnDestination(token, vendorId)),
    retry: false,
  });
  const addresses = useQuery({
    queryKey: ["vendor-addresses", vendorId],
    queryFn: () => callWithAuth((token) => api.listVendorAddresses(token, vendorId)),
  });
  const [addressId, setAddressId] = useState("");
  const [hours, setHours] = useState("");
  const [error, setError] = useState<string | null>(null);
  const save = useMutation({
    mutationFn: () =>
      callWithAuth((token) =>
        api.setReturnDestination(token, vendorId, {
          address_id: addressId || destination.data?.address_id || "",
          receiving_hours: hours.trim() || destination.data?.receiving_hours || "",
        }),
      ),
    onSuccess: async () => {
      setError(null);
      await queryClient.invalidateQueries({ queryKey: ["return-destination", vendorId] });
    },
    onError: (err) =>
      setError(err instanceof Error ? err.message : "Không lưu được địa chỉ nhận trả."),
  });
  const d = destination.data;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Địa chỉ nhận hàng trả</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2 text-sm">
        {d ? (
          <div>
            <p>
              {d.recipient_name} ({d.phone}), {d.street_address}, {d.ward}, {d.district},{" "}
              {d.province}
            </p>
            <p className="text-muted-foreground">Giờ nhận hàng: {d.receiving_hours}</p>
            <p
              className={
                d.verified
                  ? "text-xs text-emerald-700 dark:text-emerald-400"
                  : "text-xs text-destructive"
              }
            >
              {d.verified
                ? "Sàn đã xác minh"
                : "Chờ sàn xác minh; người mua chưa được gửi tới địa chỉ này"}
              {d.rejection_reason && ` · Lý do: ${d.rejection_reason}`}
            </p>
          </div>
        ) : (
          <p className="text-muted-foreground">
            Chưa chọn địa chỉ nhận hàng trả. Yêu cầu trả hàng được duyệt sẽ chờ tới khi có địa chỉ
            đã xác minh.
          </p>
        )}
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!(addressId || d?.address_id) || !(hours.trim() || d?.receiving_hours)) {
              setError("Chọn địa chỉ và nhập giờ nhận hàng.");
              return;
            }
            save.mutate();
          }}
        >
          <label className="flex flex-col gap-1 text-xs">
            Địa chỉ
            <select
              className="h-8 rounded-md border bg-background px-2"
              value={addressId || d?.address_id || ""}
              onChange={(e) => setAddressId(e.target.value)}
            >
              <option value="">Chọn địa chỉ</option>
              {(addresses.data ?? []).map((a) => (
                <option key={a.id} value={a.id}>
                  {a.recipient_name} · {a.street_address}, {a.district}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs">
            Giờ nhận hàng
            <Input
              className="h-8 w-56"
              maxLength={200}
              placeholder="8:00-17:00, thứ 2 đến thứ 6"
              value={hours || d?.receiving_hours || ""}
              onChange={(e) => setHours(e.target.value)}
            />
          </label>
          <Button type="submit" size="sm" variant="outline" disabled={save.isPending}>
            Lưu (cần sàn xác minh lại)
          </Button>
        </form>
        {error && <p className="text-xs text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
