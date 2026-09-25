"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// ProductMediaGallery previews a product's extended-description media
// (images and short videos) so the vendor can confirm what they uploaded --
// the plain photo gallery above has no equivalent preview today, but this
// new gallery gets one from the start.
export function ProductMediaGallery({ productId }: { productId: string }) {
  const { callWithAuth } = useAuth();
  const mediaQuery = useQuery({
    queryKey: ["product-media", productId],
    queryFn: () => callWithAuth((token) => api.listProductMedia(token, productId)),
  });

  if (!mediaQuery.data || mediaQuery.data.length === 0) return null;

  return (
    <div>
      <p className="text-sm font-medium">Media khác</p>
      <div className="mt-1 flex flex-wrap gap-2">
        {mediaQuery.data.map((item) =>
          item.kind === "video" ? (
            <video key={item.id} controls src={item.url} className="h-16 w-28 rounded bg-muted" />
          ) : (
            // eslint-disable-next-line @next/next/no-img-element
            <img key={item.id} src={item.url} alt="" className="h-16 w-16 rounded object-cover" />
          ),
        )}
      </div>
    </div>
  );
}

// ProductImageEditor is a product's single main image: picking a file
// uploads it right away (no separate Save step), and once it's uploaded, a
// small corner button lets the vendor delete it and pick a different one
// before submitting for review.
export function ProductImageEditor({ productId }: { productId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [isBusy, setIsBusy] = useState(false);

  const imageQuery = useQuery({
    queryKey: ["product-image", productId],
    queryFn: () => callWithAuth((token) => api.listProductImages(token, productId)),
  });
  const image = imageQuery.data?.[0];

  async function handleSelect(e: React.ChangeEvent<HTMLInputElement>) {
    const picked = e.target.files?.[0];
    e.target.value = "";
    if (!picked) return;
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth((token) => api.uploadProductImage(token, productId, picked));
      await queryClient.invalidateQueries({ queryKey: ["product-image", productId] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể tải ảnh lên.");
    } finally {
      setIsBusy(false);
    }
  }

  async function handleDelete() {
    setError(null);
    setIsBusy(true);
    try {
      await callWithAuth((token) => api.deleteProductImage(token, productId));
      await queryClient.invalidateQueries({ queryKey: ["product-image", productId] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Không thể xóa ảnh.");
    } finally {
      setIsBusy(false);
    }
  }

  return (
    <div className="flex flex-col items-start gap-1">
      {image ? (
        <div className="relative">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src={image.url} alt="" className="h-16 w-16 rounded object-cover" />
          <button
            type="button"
            onClick={handleDelete}
            disabled={isBusy}
            aria-label="Xóa ảnh"
            className="absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full bg-foreground text-[10px] leading-none text-background disabled:opacity-50"
          >
            ×
          </button>
        </div>
      ) : (
        <label className="text-sm">
          <input
            type="file"
            accept="image/*"
            className="hidden"
            onChange={handleSelect}
            disabled={isBusy}
          />
          <span className="cursor-pointer rounded-lg border border-input px-3 py-1.5 text-sm hover:bg-muted">
            {isBusy ? "Đang tải lên…" : "Chọn ảnh"}
          </span>
        </label>
      )}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

// ProductMediaUploader stages up to 5 picked files (images and/or short
// videos) at once with local previews, and only uploads them -- one at a
// time, sequentially -- when the vendor presses Save.
export function ProductMediaUploader({ productId }: { productId: string }) {
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
      setError("Bạn chỉ có thể chọn tối đa 5 tệp.");
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
      setError(err instanceof api.ApiError ? err.message : "Không thể tải media lên.");
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
        <label className="text-sm">
          <input
            type="file"
            accept="image/*,video/*"
            multiple
            className="hidden"
            onChange={handleSelect}
          />
          <span className="cursor-pointer rounded-lg border border-primary/30 px-3 py-1.5 text-sm text-primary hover:bg-primary/5">
            Chọn media (tối đa 5)
          </span>
        </label>
        {items.map((item, i) => (
          <div key={item.previewUrl} className="relative">
            {item.file.type.startsWith("video/") ? (
              <video src={item.previewUrl} className="h-10 w-16 rounded bg-muted" />
            ) : (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={item.previewUrl} alt="" className="h-10 w-10 rounded object-cover" />
            )}
            <button
              type="button"
              onClick={() => handleRemove(i)}
              aria-label="Bỏ tệp này"
              className="absolute -top-1 -right-1 flex h-4 w-4 items-center justify-center rounded-full bg-foreground text-[10px] leading-none text-background"
            >
              ×
            </button>
          </div>
        ))}
        <Button
          type="button"
          size="sm"
          onClick={handleSave}
          disabled={items.length === 0 || isSaving}
        >
          {isSaving ? "Đang lưu…" : "Lưu"}
        </Button>
      </div>
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}
