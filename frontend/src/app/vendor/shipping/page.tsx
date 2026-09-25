"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

const emptyForm: api.AddressInput = {
  recipient_name: "",
  phone: "",
  province: "",
  district: "",
  ward: "",
  street_address: "",
};

export default function VendorShippingPage() {
  const { selectedVendorId } = useAuth();

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

  return (
    <div className="flex flex-col gap-8">
      <SectionHeader as="h1" title="Thiết lập vận chuyển" />

      <ShippingMethods vendorId={selectedVendorId} />
      <WarehouseAddresses vendorId={selectedVendorId} />
    </div>
  );
}

// ShippingMethods lets a vendor enable one of admin's active carriers for
// their own shop and choose which one is the default -- the system uses
// the default carrier automatically to quote and create a shipment at
// checkout, with no buyer choice involved.
function ShippingMethods({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [carrierId, setCarrierId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const carriersQuery = useQuery({ queryKey: ["active-carriers"], queryFn: () => api.listActiveCarriers() });
  const methodsQuery = useQuery({
    queryKey: ["vendor-shipping-methods", vendorId],
    queryFn: () => callWithAuth((token) => api.listMyShippingMethods(token, vendorId)),
  });

  const carriers = carriersQuery.data ?? [];
  const methods = methodsQuery.data ?? [];
  const enabledCarrierIds = new Set(methods.map((m) => m.carrier_id));
  const availableCarriers = carriers.filter((c) => !enabledCarrierIds.has(c.id));

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-shipping-methods"] });
  }

  async function handleEnable(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await callWithAuth((token) => api.enableShippingMethod(token, vendorId, carrierId));
      setCarrierId("");
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể bật đơn vị vận chuyển này.");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleSetDefault(methodId: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.setDefaultShippingMethod(token, methodId));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể đặt làm mặc định.");
    }
  }

  async function handleSetActive(methodId: string, isActive: boolean) {
    setError(null);
    try {
      await callWithAuth((token) => api.setShippingMethodActive(token, methodId, isActive));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể cập nhật đơn vị vận chuyển.");
    }
  }

  function carrierName(id: string) {
    return carriers.find((c) => c.id === id)?.name ?? id;
  }

  return (
    <div>
      <h2 className="text-lg font-medium">Đơn vị vận chuyển</h2>
      <p className="mt-1 text-sm text-muted-foreground">
        Bật các đơn vị vận chuyển bạn sử dụng. Đơn vị mặc định sẽ tự động được dùng để báo giá và
        tạo vận đơn khi người mua thanh toán.
      </p>
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}

      <ul className="mt-4 flex flex-col gap-2">
        {methods.map((m) => (
          <li key={m.id}>
            <Card>
              <CardContent className="flex items-center justify-between">
                <span className="flex items-center gap-2 text-sm">
                  {carrierName(m.carrier_id)}
                  {m.is_default && (
                    <Badge variant="success" className="font-normal">
                      Mặc định
                    </Badge>
                  )}
                  {!m.is_active && (
                    <Badge variant="outline" className="font-normal">
                      Ngừng hoạt động
                    </Badge>
                  )}
                </span>
                <div className="flex gap-3 text-sm">
                  {!m.is_default && m.is_active && (
                    <Button variant="link" size="sm" className="h-auto p-0" onClick={() => handleSetDefault(m.id)}>
                      Đặt mặc định
                    </Button>
                  )}
                  <Button
                    variant="link"
                    size="sm"
                    className="h-auto p-0"
                    onClick={() => handleSetActive(m.id, !m.is_active)}
                  >
                    {m.is_active ? "Ngừng dùng" : "Kích hoạt"}
                  </Button>
                </div>
              </CardContent>
            </Card>
          </li>
        ))}
        {methods.length === 0 && (
          <p className="text-sm text-muted-foreground">Chưa bật đơn vị vận chuyển nào.</p>
        )}
      </ul>

      {availableCarriers.length > 0 && (
        <form onSubmit={handleEnable} className="mt-4 flex gap-2">
          <Select value={carrierId} onValueChange={setCarrierId}>
            <SelectTrigger className="w-56">
              <SelectValue placeholder="Chọn đơn vị vận chuyển…" />
            </SelectTrigger>
            <SelectContent>
              {availableCarriers.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button type="submit" disabled={isSubmitting || !carrierId}>
            Bật
          </Button>
        </form>
      )}
    </div>
  );
}

// WarehouseAddresses is where the vendor ships from -- mirrors the buyer's
// own address book. It's kept separate from shipping-fee calculation
// (which is destination-only in this pass) and is mainly for the vendor's
// own operational record.
function WarehouseAddresses({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [form, setForm] = useState<api.AddressInput>(emptyForm);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const addressesQuery = useQuery({
    queryKey: ["vendor-addresses", vendorId],
    queryFn: () => callWithAuth((token) => api.listVendorAddresses(token, vendorId)),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-addresses"] });
  }

  function startEdit(a: api.VendorAddress) {
    setEditingId(a.id);
    setForm({
      recipient_name: a.recipient_name,
      phone: a.phone,
      province: a.province,
      district: a.district,
      ward: a.ward,
      street_address: a.street_address,
    });
  }

  function cancelEdit() {
    setEditingId(null);
    setForm(emptyForm);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      if (editingId) {
        await callWithAuth((token) => api.updateVendorAddress(token, vendorId, editingId, form));
      } else {
        await callWithAuth((token) => api.addVendorAddress(token, vendorId, form));
      }
      setForm(emptyForm);
      setEditingId(null);
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể lưu địa chỉ này.");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleDelete(id: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.deleteVendorAddress(token, vendorId, id));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể xóa địa chỉ này.");
    }
  }

  async function handleSetDefault(id: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.setDefaultVendorAddress(token, vendorId, id));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể đặt làm địa chỉ mặc định.");
    }
  }

  return (
    <div>
      <h2 className="text-lg font-medium">Địa chỉ kho hàng</h2>
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}

      <ul className="mt-4 flex flex-col gap-3">
        {addressesQuery.data?.map((a) => (
          <li key={a.id}>
            <Card>
              <CardContent className="flex items-start justify-between gap-3">
                <div>
                  <div className="flex items-center gap-2">
                    <p className="font-medium">{a.recipient_name}</p>
                    {a.is_default && (
                      <Badge variant="success" className="font-normal">
                        Mặc định
                      </Badge>
                    )}
                  </div>
                  <p className="text-sm text-muted-foreground">{a.phone}</p>
                  <p className="text-sm text-muted-foreground">
                    {a.street_address}, {a.ward}, {a.district}, {a.province}
                  </p>
                </div>
                <div className="flex flex-col items-end gap-1 text-sm">
                  <Button variant="link" size="sm" className="h-auto p-0" onClick={() => startEdit(a)}>
                    Sửa
                  </Button>
                  {!a.is_default && (
                    <Button variant="link" size="sm" className="h-auto p-0" onClick={() => handleSetDefault(a.id)}>
                      Đặt mặc định
                    </Button>
                  )}
                  <Button
                    variant="link"
                    size="sm"
                    className="h-auto p-0 text-destructive"
                    onClick={() => handleDelete(a.id)}
                  >
                    Xóa
                  </Button>
                </div>
              </CardContent>
            </Card>
          </li>
        ))}
        {addressesQuery.data?.length === 0 && (
          <p className="text-sm text-muted-foreground">Chưa có địa chỉ kho hàng nào.</p>
        )}
      </ul>

      <Card className="mt-4">
        <CardContent>
          <h3 className="text-sm font-medium">
            {editingId ? "Sửa địa chỉ" : "Thêm địa chỉ kho hàng"}
          </h3>
          <form onSubmit={handleSubmit} className="mt-3 grid gap-3 sm:grid-cols-2">
            <Input
              value={form.recipient_name}
              onChange={(e) => setForm({ ...form, recipient_name: e.target.value })}
              placeholder="Tên người liên hệ"
              required
            />
            <Input
              value={form.phone}
              onChange={(e) => setForm({ ...form, phone: e.target.value })}
              placeholder="Số điện thoại"
              required
            />
            <Input
              value={form.province}
              onChange={(e) => setForm({ ...form, province: e.target.value })}
              placeholder="Tỉnh/Thành phố"
              required
            />
            <Input
              value={form.district}
              onChange={(e) => setForm({ ...form, district: e.target.value })}
              placeholder="Quận/Huyện"
              required
            />
            <Input
              value={form.ward}
              onChange={(e) => setForm({ ...form, ward: e.target.value })}
              placeholder="Phường/Xã"
              required
            />
            <Input
              value={form.street_address}
              onChange={(e) => setForm({ ...form, street_address: e.target.value })}
              placeholder="Địa chỉ cụ thể"
              required
              className="sm:col-span-2"
            />
            <div className="flex gap-2 sm:col-span-2">
              <Button type="submit" disabled={isSubmitting}>
                {isSubmitting ? "Đang lưu…" : editingId ? "Lưu thay đổi" : "Thêm địa chỉ"}
              </Button>
              {editingId && (
                <Button type="button" variant="outline" onClick={cancelEdit}>
                  Hủy
                </Button>
              )}
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
