"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { bundleLabel } from "@/lib/admin-access";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { useAdminPermissions } from "@/lib/hooks/use-admin-permissions";
import { queryKeys } from "@/lib/query-keys";

function SubjectCard({
  subject,
  bundles,
  self,
  onChanged,
}: {
  subject: api.PermissionSubject;
  bundles: string[];
  self: boolean;
  onChanged: () => Promise<void>;
}) {
  const { callWithAuth } = useAuth();
  const [bundle, setBundle] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const held = subject.grants.map((g) => g.bundle);
  const available = bundles.filter((b) => !held.includes(b));

  async function grant() {
    setBusy(true);
    try {
      await callWithAuth((t) =>
        api.grantAdminPermission(t, {
          subject_id: subject.user.id,
          bundle,
          reason: reason.trim(),
          expected_version: subject.user.permission_version,
        }),
      );
      toast.success(`Granted ${bundleLabel(bundle)}.`);
      setBundle("");
      setReason("");
    } catch (err) {
      toast.error(describeApiError(err, "Could not grant the permission."));
    } finally {
      setBusy(false);
      await onChanged();
    }
  }

  async function revoke(g: api.PermissionGrant) {
    const why = window.prompt(`Why revoke ${bundleLabel(g.bundle)} from ${subject.user.email}?`);
    if (!why?.trim()) return;
    try {
      await callWithAuth((t) =>
        api.revokeAdminPermission(t, g.id, why.trim(), subject.user.permission_version),
      );
      toast.success("Permission revoked; the next request is refused.");
    } catch (err) {
      toast.error(describeApiError(err, "Could not revoke the permission."));
    }
    await onChanged();
  }

  return (
    <li className="flex flex-col gap-2 border-b py-3 last:border-b-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{subject.user.email}</span>
        <span className="text-xs text-muted-foreground">{subject.user.full_name}</span>
        {!subject.user.is_active && <Badge variant="secondary">Inactive</Badge>}
        {self && <span className="text-xs text-muted-foreground">(you)</span>}
        <span className="ml-auto text-xs text-muted-foreground">
          version {subject.user.permission_version}
        </span>
      </div>
      <div className="flex flex-wrap gap-2">
        {subject.grants.length === 0 && (
          <span className="text-xs text-muted-foreground">No bundles</span>
        )}
        {subject.grants.map((g) => (
          <Badge key={g.id} variant="outline" className="gap-1">
            {bundleLabel(g.bundle)}
            {!self && (
              <button
                type="button"
                className="ml-1 text-destructive"
                aria-label={`Revoke ${bundleLabel(g.bundle)}`}
                onClick={() => revoke(g)}
              >
                ×
              </button>
            )}
          </Badge>
        ))}
      </div>
      {self ? (
        <p className="text-xs text-muted-foreground">
          Your own permissions are changed by another access manager.
        </p>
      ) : (
        subject.user.is_active &&
        available.length > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            <Select value={bundle} onValueChange={setBundle}>
              <SelectTrigger className="w-56">
                <SelectValue placeholder="Add a bundle" />
              </SelectTrigger>
              <SelectContent>
                {available.map((b) => (
                  <SelectItem key={b} value={b}>
                    {bundleLabel(b)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input
              className="w-64"
              placeholder="Reason (ticket, role)"
              maxLength={500}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
            <Button size="sm" disabled={busy || !bundle || !reason.trim()} onClick={grant}>
              Grant
            </Button>
          </div>
        )
      )}
    </li>
  );
}

// AccessManager lists admins and their permission bundles (AF-19). Every
// change needs a reason, applies to the version shown and is audited; no
// one changes their own bundles.
export function AccessManager() {
  const { callWithAuth, user } = useAuth();
  const queryClient = useQueryClient();
  const mine = useAdminPermissions();
  const subjects = useQuery({
    queryKey: queryKeys.permissionSubjects(),
    queryFn: () => callWithAuth((t) => api.listPermissionSubjects(t)),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: queryKeys.permissionSubjects() });
  }

  return (
    <div className="flex flex-col gap-4">
      {mine.data && !mine.data.scoped && (
        <p className="rounded-md border p-3 text-sm text-muted-foreground">
          Scoped permissions are off: every active admin still holds every bundle. Prepare the
          grants here, then turn on FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED in Identity.
        </p>
      )}
      <Card>
        <CardContent className="pt-6">
          {subjects.isPending && <p className="text-sm text-muted-foreground">Loading…</p>}
          {subjects.isError && (
            <p className="text-sm text-destructive">
              {describeApiError(subjects.error, "Could not load admin access.")}
            </p>
          )}
          <ul>
            {(subjects.data ?? []).map((s) => (
              <SubjectCard
                key={`${s.user.id}-${s.user.permission_version}`}
                subject={s}
                bundles={mine.data?.bundles ?? []}
                self={s.user.id === user?.id}
                onChanged={refresh}
              />
            ))}
          </ul>
        </CardContent>
      </Card>
    </div>
  );
}
