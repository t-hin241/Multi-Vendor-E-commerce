"use client";

import { useState } from "react";

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
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

// ReceiveReturnDialog records that returned goods arrived and were
// inspected. It starts the refund; restock puts the units back on sale.
export function ReceiveReturnDialog({
  labels = {
    trigger: "Mark received",
    title: "Goods received?",
    description: "This starts the refund. Restock only goods that can be sold again.",
    restock: "Put the units back into stock",
    note: "Inspection note",
    cancel: "Cancel",
    confirm: "Confirm received",
  },
  onConfirm,
}: {
  labels?: {
    trigger: string;
    title: string;
    description: string;
    restock: string;
    note: string;
    cancel: string;
    confirm: string;
  };
  onConfirm: (input: { restock: boolean; note: string }) => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [restock, setRestock] = useState(true);
  const [note, setNote] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    setError(null);
    setBusy(true);
    try {
      await onConfirm({ restock, note: note.trim() });
      setOpen(false);
      setNote("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button size="sm" variant="outline">
          {labels.trigger}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{labels.title}</AlertDialogTitle>
          <AlertDialogDescription>{labels.description}</AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-3">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={restock} onCheckedChange={(v) => setRestock(v === true)} />
            {labels.restock}
          </label>
          <Label htmlFor="inspection-note">{labels.note}</Label>
          <Textarea
            id="inspection-note"
            rows={3}
            maxLength={1000}
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
        </div>
        {error && <p className="text-sm text-destructive">{error}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel>{labels.cancel}</AlertDialogCancel>
          <Button disabled={busy} onClick={submit}>
            {busy ? "…" : labels.confirm}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
