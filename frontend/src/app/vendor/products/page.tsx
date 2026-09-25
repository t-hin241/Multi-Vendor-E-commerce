"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { ProductChecklistPanel } from "@/components/vendor/product-checklist-panel";
import { ProductCreateForm } from "@/components/vendor/product-create-form";
import { ProductRow } from "@/components/vendor/product-row";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { resolveActiveVendor } from "@/lib/vendor";

export default function VendorProductsPage() {
  const { callWithAuth, selectedVendorId } = useAuth();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [isSubmittingReview, setIsSubmittingReview] = useState(false);
  // Set right after a product is created, so the create form's spot shows a
  // focused "finish setting up this product" checklist instead -- no
  // scrolling down to the list to find it. Cleared once submitted for
  // review, or if the vendor chooses to finish later.
  const [justCreatedProductId, setJustCreatedProductId] = useState<string | null>(null);

  const vendorsQuery = useQuery({
    queryKey: ["my-vendors"],
    queryFn: () => callWithAuth((token) => api.listMyVendors(token)),
  });
  const vendors = vendorsQuery.data ?? [];
  const vendor = resolveActiveVendor(vendors, selectedVendorId);

  const categoriesQuery = useQuery({ queryKey: ["categories"], queryFn: api.listCategories });
  const productsQuery = useQuery({
    queryKey: ["vendor-products", vendor?.id],
    queryFn: () => callWithAuth((token) => api.listMyProducts(token, vendor!.id)),
    enabled: Boolean(vendor),
  });

  if (!vendor) {
    return (
      <p className="text-sm text-muted-foreground">
        Bạn chưa có cửa hàng nào.{" "}
        <Link href="/vendor/shops" className="text-primary underline">
          Đăng ký bán hàng
        </Link>
      </p>
    );
  }

  if (vendor.status !== "approved") {
    return (
      <p className="text-sm text-muted-foreground">
        Cửa hàng của bạn chưa được duyệt để đăng sản phẩm.{" "}
        <Link href="/vendor/shops" className="text-primary underline">
          Quản lý cửa hàng
        </Link>
      </p>
    );
  }

  async function handleToggleActive(productId: string, isActive: boolean) {
    setError(null);
    try {
      await callWithAuth((token) => api.setProductActive(token, productId, isActive));
      await queryClient.invalidateQueries({ queryKey: ["vendor-products"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể cập nhật sản phẩm.");
    }
  }

  // The backend is the authority on completeness (an image and initial
  // stock are required) -- this just surfaces its validation message rather
  // than re-deriving the same check on the client.
  async function handleSubmitForReview(productId: string) {
    setError(null);
    setIsSubmittingReview(true);
    try {
      await callWithAuth((token) => api.submitProductForReview(token, productId));
      await queryClient.invalidateQueries({ queryKey: ["vendor-products"] });
      setJustCreatedProductId((current) => (current === productId ? null : current));
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể gửi sản phẩm để duyệt.");
    } finally {
      setIsSubmittingReview(false);
    }
  }

  const justCreatedProduct = productsQuery.data?.find((p) => p.id === justCreatedProductId);
  const remainingProducts = productsQuery.data?.filter((p) => p.id !== justCreatedProductId);

  return (
    <div className="flex flex-col gap-6">
      <SectionHeader title="Sản phẩm" subtitle={vendor.shop_name} />

      {justCreatedProductId ? (
        justCreatedProduct ? (
          <ProductChecklistPanel
            vendorId={vendor.id}
            product={justCreatedProduct}
            onFinishLater={() => setJustCreatedProductId(null)}
            onSubmitForReview={() => handleSubmitForReview(justCreatedProduct.id)}
            isSubmitting={isSubmittingReview}
          />
        ) : (
          <p className="text-sm text-muted-foreground">Đang tải…</p>
        )
      ) : (
        <ProductCreateForm
          vendorId={vendor.id}
          categories={categoriesQuery.data ?? []}
          onCreated={(created) => {
            queryClient.invalidateQueries({ queryKey: ["vendor-products"] });
            setJustCreatedProductId(created.id);
          }}
        />
      )}

      {error && <p className="text-sm text-destructive">{error}</p>}

      <div className="flex flex-col gap-3">
        {remainingProducts?.map((product) => (
          <ProductRow
            key={product.id}
            vendorId={vendor.id}
            product={product}
            onToggleActive={handleToggleActive}
            onSubmitForReview={handleSubmitForReview}
          />
        ))}
        {productsQuery.data?.length === 0 && (
          <p className="text-sm text-muted-foreground">Chưa có sản phẩm nào.</p>
        )}
      </div>
    </div>
  );
}
