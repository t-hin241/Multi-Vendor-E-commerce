"use client";

import { Minus, Plus } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

// The minus/input/plus stepper, shared by product detail and cart. The
// +/- buttons commit immediately; the typed input commits on blur/Enter
// (matches cart's existing blur-commit semantics) so wiring this to a
// mutation doesn't fire a request per keystroke.
export function QuantityStepper({
  value,
  onChange,
  min = 1,
  max,
  disabled,
  ariaLabel,
  size = "default",
}: {
  value: number;
  onChange: (next: number) => void;
  min?: number;
  max?: number;
  disabled?: boolean;
  ariaLabel: string;
  size?: "default" | "sm";
}) {
  const [text, setText] = useState(() => String(value));
  // Resync the typed text when `value` changes from outside (e.g. another
  // control drives the same mutation) -- adjusted during render, same
  // pattern as the slug-reset in categories/[slug]/page.tsx, rather than an
  // effect, so it doesn't cause an extra render pass.
  const [syncedValue, setSyncedValue] = useState(value);
  if (value !== syncedValue) {
    setSyncedValue(value);
    setText(String(value));
  }

  function clamp(next: number) {
    let clamped = Number.isFinite(next) ? next : min;
    clamped = Math.max(min, clamped);
    if (max !== undefined) clamped = Math.min(max, clamped);
    return clamped;
  }

  function commit() {
    const next = clamp(Number(text));
    setText(String(next));
    if (next !== value) onChange(next);
  }

  return (
    <div className={cn("flex items-center rounded-md border", size === "sm" && "h-8")}>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        disabled={disabled || value <= min}
        onClick={() => onChange(clamp(value - 1))}
        aria-label={`Giảm ${ariaLabel}`}
      >
        <Minus className="size-4" />
      </Button>
      <Input
        type="number"
        min={min}
        max={max}
        value={text}
        disabled={disabled}
        onChange={(e) => setText(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            commit();
          }
        }}
        className="h-9 w-14 border-0 text-center shadow-none focus-visible:ring-0"
        aria-label={ariaLabel}
      />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        disabled={disabled || (max !== undefined && value >= max)}
        onClick={() => onChange(clamp(value + 1))}
        aria-label={`Tăng ${ariaLabel}`}
      >
        <Plus className="size-4" />
      </Button>
    </div>
  );
}
