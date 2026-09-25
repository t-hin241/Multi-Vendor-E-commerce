"use client";

import { useState, type ReactNode } from "react";

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

type DialogVariant = "default" | "destructive";

// Confirm button is a plain Button, not AlertDialogAction -- Action closes
// the dialog immediately on click (before an async mutation resolves), so
// controlling `open` ourselves lets a failed call keep the dialog open with
// the error visible instead of silently closing on failure.
export function ConfirmDialog({
  trigger,
  title,
  description,
  confirmLabel,
  variant = "default",
  onConfirm,
}: {
  trigger: ReactNode;
  title: string;
  description?: string;
  confirmLabel: string;
  variant?: DialogVariant;
  onConfirm: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleConfirm() {
    setError(null);
    setIsSubmitting(true);
    try {
      await onConfirm();
      setOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description && <AlertDialogDescription>{description}</AlertDialogDescription>}
        </AlertDialogHeader>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button variant={variant} disabled={isSubmitting} onClick={handleConfirm}>
            {isSubmitting ? "Working…" : confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// Same shape as ConfirmDialog plus a required reason field -- replaces
// every window.prompt("...reason...") call site (reject vendor/product/
// restock, cancel/refund order). The underlying API call is unchanged;
// only the trigger becomes a real, validated dialog.
export function ReasonDialog({
  trigger,
  title,
  description,
  confirmLabel,
  variant = "destructive",
  onConfirm,
}: {
  trigger: ReactNode;
  title: string;
  description?: string;
  confirmLabel: string;
  variant?: DialogVariant;
  onConfirm: (reason: string) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleConfirm() {
    setError(null);
    setIsSubmitting(true);
    try {
      await onConfirm(reason);
      setOpen(false);
      setReason("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setReason("");
      }}
    >
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description && <AlertDialogDescription>{description}</AlertDialogDescription>}
        </AlertDialogHeader>
        <Textarea
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="Reason…"
          rows={3}
          autoFocus
        />
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <Button
            variant={variant}
            disabled={!reason.trim() || isSubmitting}
            onClick={handleConfirm}
          >
            {isSubmitting ? "Working…" : confirmLabel}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
