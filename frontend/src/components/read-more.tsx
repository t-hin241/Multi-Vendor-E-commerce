"use client";

import { useLayoutEffect, useRef, useState, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const COLLAPSED_MAX_HEIGHT = 320; // px, ~ "max-h-80"

// Caps long content (e.g. a scraped product description) at a fixed height
// with a fade-out, only showing a "Read more" toggle when the content
// actually overflows that cap — short content renders unchanged, no toggle.
// Full literal class strings on purpose (not built via template-literal
// interpolation) so Tailwind's static scanner can actually see and emit them.
const FADE_CLASSES = {
  background:
    "after:absolute after:inset-x-0 after:bottom-0 after:h-16 after:bg-gradient-to-t after:from-background after:to-transparent",
  card: "after:absolute after:inset-x-0 after:bottom-0 after:h-16 after:bg-gradient-to-t after:from-card after:to-transparent",
} as const;

export function ReadMore({
  children,
  className,
  fadeFrom = "background",
}: {
  children: ReactNode;
  className?: string;
  /** Surface color the fade should blend into (e.g. "card" inside a Card). */
  fadeFrom?: keyof typeof FADE_CLASSES;
}) {
  const contentRef = useRef<HTMLDivElement>(null);
  const [overflows, setOverflows] = useState(false);
  const [expanded, setExpanded] = useState(false);

  useLayoutEffect(() => {
    const el = contentRef.current;
    if (el) setOverflows(el.scrollHeight > COLLAPSED_MAX_HEIGHT);
  }, [children]);

  return (
    <div className={className}>
      <div
        ref={contentRef}
        className={cn(
          "relative overflow-hidden",
          !expanded && overflows && FADE_CLASSES[fadeFrom],
        )}
        style={!expanded && overflows ? { maxHeight: COLLAPSED_MAX_HEIGHT } : undefined}
      >
        {children}
      </div>
      {overflows && (
        <Button
          variant="link"
          size="sm"
          className="mt-1 h-auto p-0"
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? "Thu gọn" : "Xem thêm"}
        </Button>
      )}
    </div>
  );
}
