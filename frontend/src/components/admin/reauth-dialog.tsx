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
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

// ReauthDialog asks for the admin's current password, gets a one-time proof
// for exactly one operation (AF-19) and hands it to onProof. It is a
// password re-check, not MFA; the password never leaves this request.
export function ReauthDialog({
  open,
  onOpenChange,
  title,
  description,
  purpose,
  operationHash,
  confirmLabel = "Confirm",
  children,
  onProof,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  purpose: string;
  operationHash: string;
  confirmLabel?: string;
  children?: React.ReactNode;
  onProof: (proof: string) => Promise<void>;
}) {
  const { callWithAuth } = useAuth();
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function close(next: boolean) {
    if (!next) {
      setPassword("");
      setError(null);
    }
    onOpenChange(next);
  }

  async function confirm(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { proof } = await callWithAuth((token) =>
        api.reauthenticate(token, { password, purpose, operation_hash: operationHash }),
      );
      setPassword("");
      await onProof(proof);
      close(false);
    } catch (err) {
      setPassword("");
      setError(describeApiError(err, "Could not complete the action."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={close}>
      <AlertDialogContent>
        <form onSubmit={confirm} className="flex flex-col gap-3">
          <AlertDialogHeader>
            <AlertDialogTitle>{title}</AlertDialogTitle>
            <AlertDialogDescription>{description}</AlertDialogDescription>
          </AlertDialogHeader>
          {children}
          <div className="flex flex-col gap-1">
            <Label htmlFor="reauth-password">Your current password</Label>
            <Input
              id="reauth-password"
              type="password"
              autoComplete="current-password"
              required
              maxLength={200}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel type="button">Cancel</AlertDialogCancel>
            <Button type="submit" disabled={busy || !password}>
              {busy ? "Working…" : confirmLabel}
            </Button>
          </AlertDialogFooter>
        </form>
      </AlertDialogContent>
    </AlertDialog>
  );
}
