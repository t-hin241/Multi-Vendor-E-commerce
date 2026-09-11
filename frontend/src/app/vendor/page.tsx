"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

function formatMoney(amount: number, currency: string) {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

export default function VendorDashboardPage() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();

  const vendorQuery = useQuery({
    queryKey: ["vendor-me"],
    queryFn: async () => {
      try {
        return await callWithAuth((token) => api.getMyVendor(token));
      } catch (err) {
        if (err instanceof api.ApiError && err.status === 404) return null;
        throw err;
      }
    },
  });

  async function handleApplied() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-me"] });
  }

  if (vendorQuery.isPending) {
    return <main className="mx-auto max-w-2xl px-6 py-12 text-sm text-slate-500">Loading…</main>;
  }

  if (!vendorQuery.data) {
    return <ApplyForm onApplied={handleApplied} />;
  }

  const vendor = vendorQuery.data;

  if (vendor.status !== "approved") {
    return (
      <main className="mx-auto max-w-2xl px-6 py-12">
        <h1 className="text-xl font-semibold text-slate-900">{vendor.shop_name}</h1>
        <p className="mt-2 text-sm font-medium capitalize text-slate-700">
          Status: {vendor.status}
        </p>
        {vendor.status === "rejected" && vendor.rejection_reason && (
          <p className="mt-2 text-sm text-red-600">Reason: {vendor.rejection_reason}</p>
        )}
        {vendor.status === "pending" && (
          <p className="mt-2 text-sm text-slate-600">Your application is awaiting admin review.</p>
        )}
      </main>
    );
  }

  return <ApprovedVendorConsole shopName={vendor.shop_name} />;
}

function ApplyForm({ onApplied }: { onApplied: () => void }) {
  const { callWithAuth } = useAuth();
  const [shopName, setShopName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await callWithAuth((token) => api.applyAsVendor(token, shopName, description));
      onApplied();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not submit application.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <main className="mx-auto max-w-sm px-6 py-12">
      <h1 className="text-xl font-semibold text-slate-900">Apply to become a vendor</h1>
      <form onSubmit={handleSubmit} className="mt-6 flex flex-col gap-4">
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Shop name
          <input
            required
            value={shopName}
            onChange={(e) => setShopName(e.target.value)}
            className="rounded border border-slate-300 px-3 py-2"
          />
        </label>
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Description
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="rounded border border-slate-300 px-3 py-2"
            rows={3}
          />
        </label>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button
          type="submit"
          disabled={isSubmitting}
          className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
        >
          {isSubmitting ? "Submitting…" : "Submit application"}
        </button>
      </form>
    </main>
  );
}

function ApprovedVendorConsole({ shopName }: { shopName: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [isCreating, setIsCreating] = useState(false);
  const [mainId, setMainId] = useState("");
  const [midId, setMidId] = useState("");
  const [subId, setSubId] = useState("");

  const categoriesQuery = useQuery({ queryKey: ["categories"], queryFn: api.listCategories });
  const productsQuery = useQuery({
    queryKey: ["vendor-products"],
    queryFn: () => callWithAuth((token) => api.listMyProducts(token)),
  });

  const categories = categoriesQuery.data ?? [];
  const mains = categories.filter((c) => c.level === 1);
  const midOptions = mainId
    ? categories.filter((c) => c.level === 2 && c.parent_id === mainId)
    : [];
  const subOptions = midId ? categories.filter((c) => c.level === 3 && c.parent_id === midId) : [];
  const categoryId = subId || midId || mainId;

  // The category's own detail fields (e.g. "Màu sắc", "Chất liệu") — the
  // set of fields, their data types and whether they're required all come
  // from this template, resolved server-side from the category's
  // inheritance chain. See AttributeManager (admin console) for how these
  // rules are authored.
  const templateQuery = useQuery({
    queryKey: ["attribute-template", categoryId],
    queryFn: () => api.getAttributeTemplate(categoryId),
    enabled: !!categoryId,
  });
  const templateFields = templateQuery.data?.attributes ?? [];
  const [attrValues, setAttrValues] = useState<Record<string, string | string[]>>({});

  function setAttrValue(attributeId: string, value: string | string[]) {
    setAttrValues((prev) => ({ ...prev, [attributeId]: value }));
  }

  function toggleMultiSelectOption(attributeId: string, optionId: string, checked: boolean) {
    setAttrValues((prev) => {
      const current = Array.isArray(prev[attributeId]) ? (prev[attributeId] as string[]) : [];
      const next = checked ? [...current, optionId] : current.filter((id) => id !== optionId);
      return { ...prev, [attributeId]: next };
    });
  }

  async function handleCreate(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    const form = new FormData(e.currentTarget);
    const name = String(form.get("name") ?? "");
    const description = String(form.get("description") ?? "");
    const priceAmount = Number(form.get("price_amount") ?? 0);

    const attributes = templateFields
      .map((field) => {
        const val = attrValues[field.attribute_id];
        if (val === undefined || val === "" || (Array.isArray(val) && val.length === 0)) return null;
        if (field.data_type === "select") return { attributeId: field.attribute_id, optionIds: [val as string] };
        if (field.data_type === "multi_select") return { attributeId: field.attribute_id, optionIds: val as string[] };
        return { attributeId: field.attribute_id, value: val as string };
      })
      .filter((v): v is NonNullable<typeof v> => v !== null);

    setIsCreating(true);
    try {
      await callWithAuth((token) =>
        api.createProduct(token, { categoryId, name, description, priceAmount, attributes }),
      );
      e.currentTarget.reset();
      setMainId("");
      setMidId("");
      setSubId("");
      setAttrValues({});
      await queryClient.invalidateQueries({ queryKey: ["vendor-products"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not create product.");
    } finally {
      setIsCreating(false);
    }
  }

  async function handleToggleActive(productId: string, isActive: boolean) {
    setError(null);
    try {
      await callWithAuth((token) => api.setProductActive(token, productId, isActive));
      await queryClient.invalidateQueries({ queryKey: ["vendor-products"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update product.");
    }
  }

  return (
    <main className="mx-auto max-w-3xl px-6 py-12">
      <h1 className="text-xl font-semibold text-slate-900">{shopName} — Products</h1>

      <form
        onSubmit={handleCreate}
        className="mt-6 flex flex-wrap items-end gap-3 rounded border border-slate-200 bg-white p-4"
      >
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Category
          <select
            value={mainId}
            onChange={(e) => {
              setMainId(e.target.value);
              setMidId("");
              setSubId("");
            }}
            required
            className="rounded border border-slate-300 px-3 py-2"
          >
            <option value="" disabled>
              Select a category
            </option>
            {mains.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </label>
        {midOptions.length > 0 && (
          <label className="flex flex-col gap-1 text-sm text-slate-700">
            Sub-category
            <select
              value={midId}
              onChange={(e) => {
                setMidId(e.target.value);
                setSubId("");
              }}
              className="rounded border border-slate-300 px-3 py-2"
            >
              <option value="">All {mains.find((c) => c.id === mainId)?.name}</option>
              {midOptions.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
        )}
        {subOptions.length > 0 && (
          <label className="flex flex-col gap-1 text-sm text-slate-700">
            Sub-sub-category
            <select
              value={subId}
              onChange={(e) => setSubId(e.target.value)}
              className="rounded border border-slate-300 px-3 py-2"
            >
              <option value="">All {midOptions.find((c) => c.id === midId)?.name}</option>
              {subOptions.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Name
          <input name="name" required className="rounded border border-slate-300 px-3 py-2" />
        </label>
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Price (VND)
          <input
            name="price_amount"
            type="number"
            min={1}
            required
            className="w-32 rounded border border-slate-300 px-3 py-2"
          />
        </label>
        <label className="flex flex-1 flex-col gap-1 text-sm text-slate-700">
          Description
          <input name="description" className="rounded border border-slate-300 px-3 py-2" />
        </label>
        {templateFields.map((field) => (
          <label key={field.attribute_id} className="flex flex-col gap-1 text-sm text-slate-700">
            {field.name}
            {field.required ? " *" : ""}
            {field.unit ? ` (${field.unit})` : ""}
            {field.data_type === "text" && (
              <input
                value={(attrValues[field.attribute_id] as string) ?? ""}
                onChange={(e) => setAttrValue(field.attribute_id, e.target.value)}
                required={field.required}
                className="rounded border border-slate-300 px-3 py-2"
              />
            )}
            {field.data_type === "number" && (
              <input
                type="number"
                value={(attrValues[field.attribute_id] as string) ?? ""}
                onChange={(e) => setAttrValue(field.attribute_id, e.target.value)}
                required={field.required}
                className="w-32 rounded border border-slate-300 px-3 py-2"
              />
            )}
            {field.data_type === "boolean" && (
              <input
                type="checkbox"
                checked={attrValues[field.attribute_id] === "true"}
                onChange={(e) => setAttrValue(field.attribute_id, e.target.checked ? "true" : "")}
                className="mt-1"
              />
            )}
            {field.data_type === "select" && (
              <select
                value={(attrValues[field.attribute_id] as string) ?? ""}
                onChange={(e) => setAttrValue(field.attribute_id, e.target.value)}
                required={field.required}
                className="rounded border border-slate-300 px-3 py-2"
              >
                <option value="" disabled>
                  Select…
                </option>
                {field.options.map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.value}
                  </option>
                ))}
              </select>
            )}
            {field.data_type === "multi_select" && (
              <div className="flex flex-col gap-1">
                {field.options.map((o) => {
                  const selected = Array.isArray(attrValues[field.attribute_id])
                    ? (attrValues[field.attribute_id] as string[]).includes(o.id)
                    : false;
                  return (
                    <label key={o.id} className="flex items-center gap-2 text-sm font-normal text-slate-700">
                      <input
                        type="checkbox"
                        checked={selected}
                        onChange={(e) => toggleMultiSelectOption(field.attribute_id, o.id, e.target.checked)}
                      />
                      {o.value}
                    </label>
                  );
                })}
              </div>
            )}
          </label>
        ))}
        <button
          type="submit"
          disabled={isCreating}
          className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
        >
          {isCreating ? "Creating…" : "Add product"}
        </button>
      </form>

      {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
      {categoriesQuery.data?.length === 0 && (
        <p className="mt-3 text-sm text-amber-600">
          No categories exist yet — ask an admin to create one before adding products.
        </p>
      )}

      <ul className="mt-6 divide-y divide-slate-200 rounded border border-slate-200 bg-white">
        {productsQuery.data?.map((product) => (
          <li key={product.id} className="flex flex-col gap-3 p-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p className="font-medium text-slate-900">{product.name}</p>
                <p className="text-sm text-slate-600">
                  {formatMoney(product.price_amount, product.currency)}
                </p>
                <p className="text-sm capitalize text-slate-500">
                  {product.status}
                  {product.status === "rejected" && product.rejection_reason
                    ? ` — ${product.rejection_reason}`
                    : ""}
                </p>
              </div>
              <div className="flex items-center gap-3">
                <label className="flex items-center gap-2 text-sm text-slate-700">
                  <input
                    type="checkbox"
                    checked={product.is_active}
                    onChange={(e) => handleToggleActive(product.id, e.target.checked)}
                  />
                  Active
                </label>
                <ProductImageUploader productId={product.id} />
                <ProductMediaUploader productId={product.id} />
              </div>
            </div>
            <ProductImagePreview productId={product.id} />
            <ProductMediaGallery productId={product.id} />
            <ProductStockManager productId={product.id} categoryId={product.category_id} />
          </li>
        ))}
      </ul>

      {productsQuery.data?.length === 0 && (
        <p className="mt-6 text-sm text-slate-500">No products yet.</p>
      )}
    </main>
  );
}

// ProductImagePreview shows a product's current single main image, so the
// vendor can confirm what's live right after an upload replaces it.
function ProductImagePreview({ productId }: { productId: string }) {
  const { callWithAuth } = useAuth();
  const imageQuery = useQuery({
    queryKey: ["product-image", productId],
    queryFn: () => callWithAuth((token) => api.listProductImages(token, productId)),
  });

  const image = imageQuery.data?.[0];
  if (!image) return null;

  // eslint-disable-next-line @next/next/no-img-element
  return <img src={image.url} alt="" className="h-16 w-16 rounded object-cover" />;
}

// ProductMediaGallery previews a product's extended-description media
// (images and short videos) so the vendor can confirm what they uploaded —
// the plain photo gallery above has no equivalent preview today, but this
// new gallery gets one from the start.
function ProductMediaGallery({ productId }: { productId: string }) {
  const { callWithAuth } = useAuth();
  const mediaQuery = useQuery({
    queryKey: ["product-media", productId],
    queryFn: () => callWithAuth((token) => api.listProductMedia(token, productId)),
  });

  if (!mediaQuery.data || mediaQuery.data.length === 0) return null;

  return (
    <div>
      <p className="text-sm font-medium text-slate-700">Other media description</p>
      <div className="mt-1 flex flex-wrap gap-2">
        {mediaQuery.data.map((item) =>
          item.kind === "video" ? (
            <video
              key={item.id}
              controls
              src={item.url}
              className="h-16 w-28 rounded bg-slate-100"
            />
          ) : (
            // eslint-disable-next-line @next/next/no-img-element
            <img key={item.id} src={item.url} alt="" className="h-16 w-16 rounded object-cover" />
          ),
        )}
      </div>
    </div>
  );
}

// ProductImageUploader stages one picked file with a local preview, and
// only actually uploads it when the vendor presses Save — the upload
// itself still replaces whatever image the product had before.
function ProductImageUploader({ productId }: { productId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  function handleSelect(e: React.ChangeEvent<HTMLInputElement>) {
    const picked = e.target.files?.[0];
    e.target.value = "";
    if (!picked) return;
    setError(null);
    if (previewUrl) URL.revokeObjectURL(previewUrl);
    setFile(picked);
    setPreviewUrl(URL.createObjectURL(picked));
  }

  async function handleSave() {
    if (!file) return;
    setIsSaving(true);
    setError(null);
    try {
      await callWithAuth((token) => api.uploadProductImage(token, productId, file));
      await queryClient.invalidateQueries({ queryKey: ["product-image", productId] });
      if (previewUrl) URL.revokeObjectURL(previewUrl);
      setFile(null);
      setPreviewUrl(null);
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not upload image.");
    } finally {
      setIsSaving(false);
    }
  }

  useEffect(() => {
    return () => {
      if (previewUrl) URL.revokeObjectURL(previewUrl);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="flex flex-col items-start gap-1">
      <div className="flex items-center gap-2">
        <label className="text-sm text-slate-600">
          <input type="file" accept="image/*" className="hidden" onChange={handleSelect} />
          <span className="cursor-pointer rounded border border-slate-300 px-3 py-1.5">
            Choose image
          </span>
        </label>
        {previewUrl && (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={previewUrl} alt="" className="h-10 w-10 rounded object-cover" />
        )}
        <button
          type="button"
          onClick={handleSave}
          disabled={!file || isSaving}
          className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        >
          {isSaving ? "Saving…" : "Save"}
        </button>
      </div>
      <p className="text-xs text-slate-400">Saving a new image replaces the current one.</p>
      {error && <p className="text-xs text-red-600">{error}</p>}
    </div>
  );
}

// ProductMediaUploader stages up to 5 picked files (images and/or short
// videos) at once with local previews, and only uploads them — one at a
// time, sequentially — when the vendor presses Save.
function ProductMediaUploader({ productId }: { productId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [items, setItems] = useState<{ file: File; previewUrl: string }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  function handleSelect(e: React.ChangeEvent<HTMLInputElement>) {
    const picked = Array.from(e.target.files ?? []);
    e.target.value = "";
    if (picked.length === 0) return;
    if (picked.length > 5) {
      setError("You can select up to 5 files at once.");
      return;
    }
    setError(null);
    items.forEach((it) => URL.revokeObjectURL(it.previewUrl));
    setItems(picked.map((file) => ({ file, previewUrl: URL.createObjectURL(file) })));
  }

  function handleRemove(index: number) {
    setItems((prev) => {
      const target = prev[index];
      if (target) URL.revokeObjectURL(target.previewUrl);
      return prev.filter((_, i) => i !== index);
    });
  }

  async function handleSave() {
    if (items.length === 0) return;
    setIsSaving(true);
    setError(null);
    try {
      // Sequential, not Promise.all: each upload's position comes from a
      // count-then-insert check on the backend (see UploadMedia), so
      // concurrent requests could race past the 5-item cap. Awaiting one
      // at a time avoids that and keeps upload order predictable.
      for (const item of items) {
        await callWithAuth((token) => api.uploadProductMedia(token, productId, item.file));
        URL.revokeObjectURL(item.previewUrl);
        setItems((prev) => prev.filter((it) => it !== item));
      }
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not upload media.");
    } finally {
      await queryClient.invalidateQueries({ queryKey: ["product-media", productId] });
      setIsSaving(false);
    }
  }

  useEffect(() => {
    return () => {
      items.forEach((it) => URL.revokeObjectURL(it.previewUrl));
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="flex flex-col items-start gap-1">
      <div className="flex flex-wrap items-center gap-2">
        <label className="text-sm text-slate-600">
          <input
            type="file"
            accept="image/*,video/*"
            multiple
            className="hidden"
            onChange={handleSelect}
          />
          <span className="cursor-pointer rounded border border-indigo-300 px-3 py-1.5 text-indigo-700">
            Choose media (up to 5)
          </span>
        </label>
        {items.map((item, i) => (
          <div key={item.previewUrl} className="relative">
            {item.file.type.startsWith("video/") ? (
              <video src={item.previewUrl} className="h-10 w-16 rounded bg-slate-100" />
            ) : (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={item.previewUrl} alt="" className="h-10 w-10 rounded object-cover" />
            )}
            <button
              type="button"
              onClick={() => handleRemove(i)}
              className="absolute -right-1 -top-1 h-4 w-4 rounded-full bg-slate-900 text-[10px] leading-none text-white"
            >
              ×
            </button>
          </div>
        ))}
        <button
          type="button"
          onClick={handleSave}
          disabled={items.length === 0 || isSaving}
          className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        >
          {isSaving ? "Saving…" : "Save"}
        </button>
      </div>
      {error && <p className="text-xs text-red-600">{error}</p>}
    </div>
  );
}

function slugify(text: string): string {
  const withoutDiacritics = Array.from(text.normalize("NFD"))
    .filter((ch) => {
      const code = ch.codePointAt(0) ?? 0;
      return code < 0x300 || code > 0x36f;
    })
    .join("");
  return withoutDiacritics
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

// ProductStockManager is the vendor's stock-input control for one product.
// A category with no variant-defining attributes gets the plain
// product-level Set-stock/+Add-stock control; a category with them shows
// the variant matrix (once generated) or the generator itself.
function ProductStockManager({ productId, categoryId }: { productId: string; categoryId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();

  const templateQuery = useQuery({
    queryKey: ["attribute-template", categoryId],
    queryFn: () => api.getAttributeTemplate(categoryId),
    enabled: !!categoryId,
  });
  const variantAxes = (templateQuery.data?.attributes ?? []).filter((f) => f.is_variant_defining);

  const variantsQuery = useQuery({
    queryKey: ["product-variants", productId],
    queryFn: () => callWithAuth((token) => api.listProductVariants(token, productId)),
    enabled: variantAxes.length > 0,
  });

  // Shared across every product row on this page — React Query dedupes
  // identical keys, so this is one fetch, not one per row.
  const inventoryQuery = useQuery({
    queryKey: ["vendor-inventory"],
    queryFn: () => callWithAuth((token) => api.listMyInventory(token)),
  });

  function invalidateInventory() {
    return queryClient.invalidateQueries({ queryKey: ["vendor-inventory"] });
  }

  if (variantAxes.length === 0) {
    const item = inventoryQuery.data?.find((i) => i.product_id === productId && !i.variant_id);
    return <PlainStockEditor productId={productId} item={item} onChanged={invalidateInventory} />;
  }

  if (!variantsQuery.data || variantsQuery.data.length === 0) {
    return (
      <VariantGenerator
        productId={productId}
        axes={variantAxes}
        onCreated={() => {
          queryClient.invalidateQueries({ queryKey: ["product-variants", productId] });
          invalidateInventory();
        }}
      />
    );
  }

  const inventoryByVariant = new Map(
    (inventoryQuery.data ?? []).filter((i) => i.variant_id).map((i) => [i.variant_id as string, i]),
  );

  return (
    <div className="mt-1">
      <p className="text-sm font-medium text-slate-700">Variants</p>
      <ul className="mt-1 space-y-1.5">
        {variantsQuery.data.map((v) => (
          <VariantStockRow key={v.id} variant={v} item={inventoryByVariant.get(v.id)} onChanged={invalidateInventory} />
        ))}
      </ul>
    </div>
  );
}

// PlainStockEditor handles product-level stock (no variants): a first
// "Set stock" input while no inventory item exists yet, then a running
// total plus a "+ Add stock" input once it does.
function PlainStockEditor({
  productId,
  item,
  onChanged,
}: {
  productId: string;
  item?: api.InventoryItem;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [quantity, setQuantity] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  async function handleSubmit() {
    setError(null);
    setIsSaving(true);
    try {
      if (item) {
        await callWithAuth((token) => api.restockProduct(token, productId, Number(quantity)));
      } else {
        await callWithAuth((token) => api.createInventoryItemForProduct(token, productId, Number(quantity)));
      }
      setQuantity("");
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update stock.");
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <div className="flex flex-col items-start gap-1">
      <div className="flex items-center gap-2">
        {item && <span className="text-sm text-slate-700">In stock: {item.available_quantity}</span>}
        <input
          type="number"
          min={item ? 1 : 0}
          value={quantity}
          onChange={(e) => setQuantity(e.target.value)}
          placeholder={item ? "Quantity" : "Set stock"}
          className="w-24 rounded border border-slate-300 px-2 py-1 text-sm"
        />
        <button
          type="button"
          onClick={handleSubmit}
          disabled={quantity === "" || isSaving}
          className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
        >
          {isSaving ? "Saving…" : item ? "+ Add stock" : "Set stock"}
        </button>
      </div>
      {error && <p className="text-xs text-red-600">{error}</p>}
    </div>
  );
}

// VariantStockRow shows one already-created variant. It can still lack an
// inventory row (e.g. the create-variants sequence was interrupted before
// reaching it), so it falls back to a "Set stock" control in that case,
// mirroring PlainStockEditor.
function VariantStockRow({
  variant,
  item,
  onChanged,
}: {
  variant: api.ProductVariant;
  item?: api.InventoryItem;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [quantity, setQuantity] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  const label = variant.options.map((o) => `${o.attribute_name}: ${o.option_value}`).join(", ");

  async function handleSubmit() {
    setError(null);
    setIsSaving(true);
    try {
      if (item) {
        await callWithAuth((token) => api.restockVariant(token, variant.id, Number(quantity)));
      } else {
        await callWithAuth((token) => api.createInventoryItemForVariant(token, variant.id, Number(quantity)));
      }
      setQuantity("");
      onChanged();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update stock.");
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <li className="flex flex-wrap items-center gap-2 text-sm text-slate-700">
      <span>
        {variant.sku} — {label} — In stock: {item?.available_quantity ?? 0}
      </span>
      <input
        type="number"
        min={item ? 1 : 0}
        value={quantity}
        onChange={(e) => setQuantity(e.target.value)}
        className="w-20 rounded border border-slate-300 px-2 py-1 text-sm"
      />
      <button
        type="button"
        onClick={handleSubmit}
        disabled={quantity === "" || isSaving}
        className="rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
      >
        {isSaving ? "Saving…" : item ? "+ Add stock" : "Set stock"}
      </button>
      {error && <span className="text-xs text-red-600">{error}</span>}
    </li>
  );
}

// VariantGenerator lets the vendor pick which option values apply to this
// product for each variant-defining attribute (e.g. Size: S, M; Color: Red)
// and builds the Cartesian product into an editable SKU + initial-stock
// table, then creates each variant (and its stock) sequentially — same
// rationale as ProductMediaUploader's sequential uploads: predictable
// order, no racing the backend's sku/combination uniqueness checks.
function VariantGenerator({
  productId,
  axes,
  onCreated,
}: {
  productId: string;
  axes: api.AttributeTemplateField[];
  onCreated: () => void;
}) {
  const { callWithAuth } = useAuth();
  const [selectedOptions, setSelectedOptions] = useState<Record<string, string[]>>({});
  const [rows, setRows] = useState<{ optionIds: string[]; label: string; sku: string; quantity: string }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  function toggleOption(attributeId: string, optionId: string, checked: boolean) {
    setSelectedOptions((prev) => {
      const current = prev[attributeId] ?? [];
      const next = checked ? [...current, optionId] : current.filter((id) => id !== optionId);
      return { ...prev, [attributeId]: next };
    });
  }

  function handleGenerate() {
    setError(null);
    const axisChoices = axes.map((axis) =>
      (selectedOptions[axis.attribute_id] ?? []).map((optionId) => ({
        optionId,
        value: axis.options.find((o) => o.id === optionId)?.value ?? optionId,
      })),
    );
    if (axisChoices.some((choices) => choices.length === 0)) {
      setError("Select at least one option for every variant attribute.");
      return;
    }

    let combinations: { optionId: string; value: string }[][] = [[]];
    for (const choices of axisChoices) {
      const next: { optionId: string; value: string }[][] = [];
      for (const combo of combinations) {
        for (const choice of choices) {
          next.push([...combo, choice]);
        }
      }
      combinations = next;
    }

    setRows(
      combinations.map((combo) => ({
        optionIds: combo.map((c) => c.optionId),
        label: combo.map((c) => c.value).join(" / "),
        sku: slugify(combo.map((c) => c.value).join("-")),
        quantity: "",
      })),
    );
  }

  function updateRow(index: number, field: "sku" | "quantity", value: string) {
    setRows((prev) => prev.map((row, i) => (i === index ? { ...row, [field]: value } : row)));
  }

  async function handleCreateVariants() {
    setError(null);
    setIsSaving(true);
    try {
      for (const row of rows) {
        await callWithAuth(async (token) => {
          const variant = await api.createProductVariant(token, productId, row.sku, row.optionIds);
          await api.createInventoryItemForVariant(token, variant.id, Number(row.quantity || 0));
        });
      }
      setRows([]);
      setSelectedOptions({});
      onCreated();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not create variants.");
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <div className="mt-1 rounded border border-slate-200 p-3">
      <p className="text-sm font-medium text-slate-700">Set up variants</p>
      <div className="mt-2 flex flex-wrap gap-4">
        {axes.map((axis) => (
          <div key={axis.attribute_id}>
            <p className="text-xs font-medium text-slate-600">{axis.name}</p>
            <div className="mt-1 flex flex-wrap gap-2">
              {axis.options.map((o) => (
                <label key={o.id} className="flex items-center gap-1 text-xs text-slate-700">
                  <input
                    type="checkbox"
                    checked={(selectedOptions[axis.attribute_id] ?? []).includes(o.id)}
                    onChange={(e) => toggleOption(axis.attribute_id, o.id, e.target.checked)}
                  />
                  {o.value}
                </label>
              ))}
            </div>
          </div>
        ))}
      </div>
      <button
        type="button"
        onClick={handleGenerate}
        className="mt-2 rounded border border-slate-300 px-3 py-1.5 text-sm text-slate-700"
      >
        Generate variants
      </button>

      {rows.length > 0 && (
        <div className="mt-3 overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs text-slate-500">
                <th className="pb-1 pr-2">Variant</th>
                <th className="pb-1 pr-2">SKU</th>
                <th className="pb-1">Initial stock</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row, i) => (
                <tr key={row.optionIds.join(",")}>
                  <td className="py-1 pr-2">{row.label}</td>
                  <td className="py-1 pr-2">
                    <input
                      value={row.sku}
                      onChange={(e) => updateRow(i, "sku", e.target.value)}
                      className="w-32 rounded border border-slate-300 px-2 py-1"
                    />
                  </td>
                  <td className="py-1">
                    <input
                      type="number"
                      min={0}
                      value={row.quantity}
                      onChange={(e) => updateRow(i, "quantity", e.target.value)}
                      className="w-20 rounded border border-slate-300 px-2 py-1"
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <button
            type="button"
            onClick={handleCreateVariants}
            disabled={isSaving}
            className="mt-2 rounded bg-slate-900 px-3 py-1.5 text-sm font-medium text-white disabled:opacity-50"
          >
            {isSaving ? "Creating…" : "Create variants"}
          </button>
        </div>
      )}
      {error && <p className="mt-2 text-xs text-red-600">{error}</p>}
    </div>
  );
}
