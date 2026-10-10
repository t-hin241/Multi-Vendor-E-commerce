"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// SecuritySettings sets up the admin's authenticator app (PW-028): the
// secret and the recovery codes are shown once, kept only in this page's
// memory, and never sent anywhere else.
export function SecuritySettings() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const status = useQuery({
    queryKey: ["admin-mfa"],
    queryFn: () => callWithAuth((token) => api.getMFAStatus(token)),
  });
  const [enrollment, setEnrollment] = useState<api.TOTPEnrollment | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  const s = status.data;
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Two-step verification"
        subtitle="Money operations ask for your password and a code from your authenticator app."
      />
      <Card>
        <CardContent className="flex flex-col gap-3 pt-6 text-sm">
          {status.isPending && <p className="text-muted-foreground">Loading…</p>}
          {s && s.enrolled && !recovery && (
            <p>An authenticator app is set up. Recovery codes left: {s.recovery_codes_left}.</p>
          )}
          {s && !s.enrolled && !s.enrollment_possible && (
            <p className="text-muted-foreground">
              Two-step verification is not configured on this platform yet.
            </p>
          )}
          {s && !s.enrolled && s.enrollment_possible && !enrollment && (
            <Button
              className="self-start"
              disabled={busy}
              onClick={() =>
                run(async () => setEnrollment(await callWithAuth((t) => api.startTOTP(t))))
              }
            >
              Set up an authenticator app
            </Button>
          )}
          {enrollment && !recovery && (
            <form
              className="flex flex-col gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                run(async () => {
                  const out = await callWithAuth((t) => api.confirmTOTP(t, code.trim()));
                  setRecovery(out.recovery_codes);
                  setEnrollment(null);
                  setCode("");
                  await queryClient.invalidateQueries({ queryKey: ["admin-mfa"] });
                });
              }}
            >
              <p>
                Add this key to your authenticator app (or open the link on the phone), then enter
                the 6-digit code it shows.
              </p>
              <p className="font-mono text-xs break-all" aria-label="Authenticator key">
                {enrollment.secret}
              </p>
              <a className="text-xs break-all text-primary underline" href={enrollment.otpauth_uri}>
                {enrollment.otpauth_uri}
              </a>
              <Label htmlFor="totp-code">Code from the app</Label>
              <Input
                id="totp-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                className="w-32"
                value={code}
                onChange={(e) => setCode(e.target.value)}
              />
              <Button
                type="submit"
                className="self-start"
                disabled={busy || code.trim().length !== 6}
              >
                Confirm
              </Button>
            </form>
          )}
          {recovery && (
            <div className="flex flex-col gap-2">
              <p className="font-medium">
                Save these recovery codes now; each works once if you lose your phone. They will not
                be shown again.
              </p>
              <ul className="grid grid-cols-2 gap-1 font-mono text-xs">
                {recovery.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
              <Button variant="outline" className="self-start" onClick={() => setRecovery(null)}>
                I saved them
              </Button>
            </div>
          )}
          <ActionError error={error} />
        </CardContent>
      </Card>
    </div>
  );
}
