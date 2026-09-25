"use client";

import { ChevronLeft, ChevronRight, PlayCircle } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import type { MediaKind } from "@/lib/api-client";
import { cn } from "@/lib/utils";

export type GalleryItem = { id: string; kind: MediaKind; url: string };

// A single viewer for every product image/video (no separate "other media"
// section) -- next/previous arrows over the main frame plus a thumbnail
// strip, so browsing extra shots doesn't cost page height.
export function ProductGallery({ items, alt }: { items: GalleryItem[]; alt: string }) {
  const [index, setIndex] = useState(0);

  function go(delta: number) {
    setIndex((i) => (i + delta + items.length) % items.length);
  }

  if (items.length === 0) {
    return <div className="aspect-square w-full rounded-lg border bg-muted" />;
  }

  // Safe by construction past this point: `go` wraps via modulo `items.length`
  // and the initial state is 0, both always within [0, items.length).
  const active = items[Math.min(index, items.length - 1)]!;

  return (
    <div>
      <div className="relative aspect-square w-full overflow-hidden rounded-lg border bg-muted">
        {active.kind === "video" ? (
          <video key={active.id} src={active.url} controls className="size-full object-cover" />
        ) : (
          // eslint-disable-next-line @next/next/no-img-element
          <img key={active.id} src={active.url} alt={alt} className="size-full object-cover" />
        )}
        {items.length > 1 && (
          <>
            <Button
              type="button"
              variant="secondary"
              size="icon"
              className="absolute top-1/2 left-2 -translate-y-1/2 rounded-full opacity-90"
              onClick={() => go(-1)}
              aria-label="Previous media"
            >
              <ChevronLeft />
            </Button>
            <Button
              type="button"
              variant="secondary"
              size="icon"
              className="absolute top-1/2 right-2 -translate-y-1/2 rounded-full opacity-90"
              onClick={() => go(1)}
              aria-label="Next media"
            >
              <ChevronRight />
            </Button>
          </>
        )}
      </div>

      {items.length > 1 && (
        <div className="mt-3 flex gap-2 overflow-x-auto pb-1">
          {items.map((item, i) => (
            <button
              key={item.id}
              type="button"
              onClick={() => setIndex(i)}
              className={cn(
                "size-16 shrink-0 overflow-hidden rounded-md border-2 transition-colors",
                i === index ? "border-primary" : "border-transparent",
              )}
              aria-label={`View media ${i + 1}`}
              aria-current={i === index}
            >
              {item.kind === "video" ? (
                <span className="flex size-full items-center justify-center bg-muted">
                  <PlayCircle className="size-6 text-muted-foreground" />
                </span>
              ) : (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={item.url} alt="" className="size-full object-cover" />
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
