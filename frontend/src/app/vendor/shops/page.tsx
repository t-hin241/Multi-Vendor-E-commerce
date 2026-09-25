"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ImageOff } from "lucide-react";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { VendorStatusBadge } from "@/components/vendor/status-badges";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// My Shops: a vendor user may own several shops (1:N) -- this page lists
// every one the caller owns (any status), lets them switch which shop
// every other vendor page acts on, edit a shop's name/description, and
// apply for an additional shop.
export default function VendorShopsPage() {
  const { callWithAuth, selectedVendorId, setSelectedVendorId } = useAuth();
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editForm, setEditForm] = useState({ shopName: "", description: "", policyText: "" });
  const [applyForm, setApplyForm] = useState({ shopName: "", description: "" });
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const vendorsQuery = useQuery({
    queryKey: ["my-vendors"],
    queryFn: () => callWithAuth((token) => api.listMyVendors(token)),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["my-vendors"] });
  }

  function startEdit(v: api.Vendor) {
    setEditingId(v.id);
    setEditForm({ shopName: v.shop_name, description: v.description, policyText: v.policy_text });
  }

  function cancelEdit() {
    setEditingId(null);
  }

  async function handleSaveEdit(e: React.FormEvent) {
    e.preventDefault();
    if (!editingId) return;
    setError(null);
    setIsSubmitting(true);
    try {
      await callWithAuth((token) =>
        api.updateVendor(token, editingId, editForm.shopName, editForm.description, editForm.policyText),
      );
      setEditingId(null);
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể lưu cửa hàng này.");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleApply(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      const created = await callWithAuth((token) =>
        api.applyAsVendor(token, applyForm.shopName, applyForm.description),
      );
      setApplyForm({ shopName: "", description: "" });
      setSelectedVendorId(created.id);
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể gửi đơn đăng ký.");
    } finally {
      setIsSubmitting(false);
    }
  }

  const vendors = vendorsQuery.data ?? [];

  return (
    <div className="flex flex-col gap-6">
      <SectionHeader
        as="h1"
        title="Cửa hàng của tôi"
        subtitle="Bạn có thể sở hữu nhiều cửa hàng. Chọn cửa hàng đang hoạt động — mọi trang vendor khác (sản phẩm, đơn hàng, tồn kho, vận chuyển) chỉ tác động lên cửa hàng đang hoạt động."
      />
      {error && <p className="text-sm text-destructive">{error}</p>}

      <ul className="flex flex-col gap-3">
        {vendors.map((v) => {
          const isActive = v.id === selectedVendorId;
          const isEditing = editingId === v.id;
          return (
            <li key={v.id}>
              <Card className={isActive ? "border-primary" : undefined}>
                <CardContent>
                  {isEditing ? (
                    <form onSubmit={handleSaveEdit} className="flex flex-col gap-2">
                      <Input
                        value={editForm.shopName}
                        onChange={(e) => setEditForm({ ...editForm, shopName: e.target.value })}
                        required
                      />
                      <Textarea
                        value={editForm.description}
                        onChange={(e) => setEditForm({ ...editForm, description: e.target.value })}
                        rows={2}
                        placeholder="Mô tả"
                      />
                      <Label className="flex flex-col items-start gap-1.5 text-sm">
                        Chính sách đổi trả / vận chuyển
                        <Textarea
                          value={editForm.policyText}
                          onChange={(e) => setEditForm({ ...editForm, policyText: e.target.value })}
                          rows={3}
                          placeholder="Hiển thị trên trang cửa hàng công khai"
                        />
                      </Label>
                      <div className="flex gap-2">
                        <Button type="submit" size="sm" disabled={isSubmitting}>
                          Lưu
                        </Button>
                        <Button type="button" variant="outline" size="sm" onClick={cancelEdit}>
                          Hủy
                        </Button>
                      </div>
                    </form>
                  ) : (
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <div className="flex items-center gap-2">
                          <p className="font-medium">{v.shop_name}</p>
                          {isActive && (
                            <span className="text-xs text-success">(đang hoạt động)</span>
                          )}
                        </div>
                        <div className="mt-1">
                          <VendorStatusBadge status={v.status} />
                        </div>
                        {v.status === "rejected" && v.rejection_reason && (
                          <p className="mt-1 text-sm text-destructive">Lý do: {v.rejection_reason}</p>
                        )}
                        {v.description && (
                          <p className="mt-1 text-sm text-muted-foreground">{v.description}</p>
                        )}
                      </div>
                      <div className="flex flex-col items-end gap-1 text-sm">
                        {!isActive && (
                          <Button variant="link" size="sm" className="h-auto p-0" onClick={() => setSelectedVendorId(v.id)}>
                            Chuyển sang cửa hàng này
                          </Button>
                        )}
                        <Button variant="link" size="sm" className="h-auto p-0" onClick={() => startEdit(v)}>
                          Sửa
                        </Button>
                      </div>
                    </div>
                  )}
                  <VendorBrandingUploader vendor={v} onChanged={refresh} />
                </CardContent>
              </Card>
            </li>
          );
        })}
        {vendors.length === 0 && !vendorsQuery.isPending && (
          <p className="text-sm text-muted-foreground">Bạn chưa có cửa hàng nào.</p>
        )}
      </ul>

      <Card>
        <CardContent>
          <h2 className="text-sm font-medium">Đăng ký cửa hàng mới</h2>
          <form onSubmit={handleApply} className="mt-3 flex flex-col gap-3">
            <Input
              value={applyForm.shopName}
              onChange={(e) => setApplyForm({ ...applyForm, shopName: e.target.value })}
              placeholder="Tên cửa hàng"
              required
            />
            <Textarea
              value={applyForm.description}
              onChange={(e) => setApplyForm({ ...applyForm, description: e.target.value })}
              placeholder="Mô tả"
              rows={3}
            />
            <Button type="submit" disabled={isSubmitting} className="self-start">
              {isSubmitting ? "Đang gửi…" : "Gửi đơn đăng ký"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}

// VendorBrandingUploader: picking a file uploads it right away (no separate
// Save step) -- same idiom as ProductImageEditor for product photos.
// Independent of the shop-name/description edit form above, so it's always
// visible, not just while editing.
function VendorBrandingUploader({
  vendor,
  onChanged,
}: {
  vendor: api.Vendor;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"logo" | "banner" | null>(null);

  async function handleUpload(kind: "logo" | "banner", file: File) {
    setError(null);
    setBusy(kind);
    try {
      await callWithAuth((token) =>
        kind === "logo"
          ? api.uploadVendorLogo(token, vendor.id, file)
          : api.uploadVendorBanner(token, vendor.id, file),
      );
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể tải ảnh lên.");
    } finally {
      setBusy(null);
    }
  }

  function pickHandler(kind: "logo" | "banner") {
    return (e: React.ChangeEvent<HTMLInputElement>) => {
      const picked = e.target.files?.[0];
      e.target.value = "";
      if (picked) handleUpload(kind, picked);
    };
  }

  return (
    <div className="mt-3 flex flex-wrap items-center gap-4 border-t pt-3">
      <Label className="flex flex-col items-start gap-1.5 text-xs">
        Logo
        <div className="flex items-center gap-2">
          <div className="flex size-10 items-center justify-center overflow-hidden rounded-full border bg-muted">
            {vendor.logo_url ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={vendor.logo_url} alt="" className="size-full object-cover" />
            ) : (
              <ImageOff className="size-4 text-muted-foreground" />
            )}
          </div>
          <input
            type="file"
            accept="image/jpeg,image/png,image/webp"
            onChange={pickHandler("logo")}
            disabled={busy === "logo"}
            className="text-xs"
          />
        </div>
      </Label>
      <Label className="flex flex-col items-start gap-1.5 text-xs">
        Banner
        <div className="flex items-center gap-2">
          <div className="flex h-10 w-16 items-center justify-center overflow-hidden rounded border bg-muted">
            {vendor.banner_url ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={vendor.banner_url} alt="" className="size-full object-cover" />
            ) : (
              <ImageOff className="size-4 text-muted-foreground" />
            )}
          </div>
          <input
            type="file"
            accept="image/jpeg,image/png,image/webp"
            onChange={pickHandler("banner")}
            disabled={busy === "banner"}
            className="text-xs"
          />
        </div>
      </Label>
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}
