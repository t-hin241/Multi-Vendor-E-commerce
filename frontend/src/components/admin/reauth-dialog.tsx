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

// ReauthDialog asks for the admin's current password (and, once two-step
// verification is set up, the authenticator code, PW-028), gets a one-time
// proof for exactly one operation (AF-19) and hands it to onProof. The
// password and code never leave this request.
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
  const [otp, setOtp] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function close(next: boolean) {
    if (!next) {
      setPassword("");
      setOtp("");
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
        api.reauthenticate(token, {
          password,
          purpose,
          operation_hash: operationHash,
          otp_code: otp.trim() || undefined,
        }),
      );
      setPassword("");
      setOtp("");
      await onProof(proof);
      close(false);
    } catch (err) {
      setPassword("");
      setOtp("");
      setError(
        err instanceof api.ApiError && err.code === "mfa_enrollment_required"
          ? "Set up an authenticator app first (Admin → My security)."
          : describeApiError(err, "Could not complete the action."),
      );
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
          <div className="flex flex-col gap-1">
            <Label htmlFor="reauth-otp">Authenticator code (if two-step verification is on)</Label>
            <Input
              id="reauth-otp"
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={11}
              value={otp}
              onChange={(e) => setOtp(e.target.value)}
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
