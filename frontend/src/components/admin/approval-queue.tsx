"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { ReauthDialog } from "@/components/admin/reauth-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  APPROVAL_DECIDE_PURPOSE,
  APPROVAL_KIND_LABELS,
  APPROVAL_SUBMIT_PURPOSE,
} from "@/lib/admin-access";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { useAdminPermissions } from "@/lib/hooks/use-admin-permissions";
import { queryKeys } from "@/lib/query-keys";

const STATUSES = ["pending", "draft", "approved", "rejected", "expired", "cancelled", ""] as const;

type Action = { request: api.ApprovalRequest; kind: "submit" | "approve" | "reject" } | null;

function Payload({ request }: { request: api.ApprovalRequest }) {
  return (
    <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs">
      <dt className="text-muted-foreground">Target</dt>
      <dd className="font-mono break-all">{request.target_id}</dd>
      {Object.entries(request.payload).map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="break-all">{String(v)}</dd>
        </div>
      ))}
      {Object.entries(request.snapshot).map(([k, v]) => (
        <div key={`s-${k}`} className="contents">
          <dt className="text-muted-foreground">at preparation: {k}</dt>
          <dd className="break-all">{String(v)}</dd>
        </div>
      ))}
    </dl>
  );
}

// ApprovalQueue is the maker-checker queue of manual money actions (AF-19):
// the preparing admin confirms the password to submit; a different admin
// with finance.approve confirms the password to approve, and the action
// runs with that approval.
export function ApprovalQueue() {
  const { callWithAuth, user } = useAuth();
  const queryClient = useQueryClient();
  const mine = useAdminPermissions();
  const [status, setStatus] = useState<string>("pending");
  const [action, setAction] = useState<Action>(null);
  const [reason, setReason] = useState("");
  const query = useQuery({
    queryKey: queryKeys.approvalRequests(status),
    queryFn: () => callWithAuth((t) => api.listApprovalRequests(t, status)),
  });
  const can = (b: string) => Boolean(mine.data?.permissions.includes(b));

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["approval-requests"] });
  }

  async function act(proof: string) {
    if (!action) return;
    const r = action.request;
    if (action.kind === "submit") {
      await callWithAuth((t) => api.submitApprovalRequest(t, r.id, proof, r.version));
      toast.success("Submitted for approval.");
    } else {
      await callWithAuth((t) =>
        api.decideApprovalRequest(t, r.id, {
          decision: action.kind === "approve" ? "approve" : "reject",
          proof,
          expected_version: r.version,
          reason: reason.trim(),
        }),
      );
      toast.success(action.kind === "approve" ? "Approved and executed." : "Rejected.");
    }
    setReason("");
    await refresh();
  }

  async function cancel(r: api.ApprovalRequest) {
    if (!window.confirm("Cancel this request?")) return;
    try {
      await callWithAuth((t) => api.cancelApprovalRequest(t, r.id, r.version));
      toast.success("Request cancelled.");
    } catch (err) {
      toast.error(describeApiError(err, "Could not cancel the request."));
    }
    await refresh();
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap gap-2">
        {STATUSES.map((s) => (
          <Button
            key={s || "all"}
            size="sm"
            variant={status === s ? "default" : "outline"}
            onClick={() => setStatus(s)}
          >
            {s || "all"}
          </Button>
        ))}
      </div>
      {query.data && !query.data.enabled && (
        <p className="rounded-md border p-3 text-sm text-muted-foreground">
          Approvals are off: refund and payout results are still recorded directly by one admin.
        </p>
      )}
      {query.isError && (
        <p className="text-sm text-destructive">
          {describeApiError(query.error, "Could not load approval requests.")}
        </p>
      )}
      {query.data?.items.length === 0 && (
        <p className="text-sm text-muted-foreground">No requests.</p>
      )}
      {query.data?.items.map((r) => {
        const isMaker = r.maker_id === user?.id;
        return (
          <Card key={r.id}>
            <CardContent className="flex flex-col gap-2 pt-6">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{APPROVAL_KIND_LABELS[r.operation_kind]}</span>
                <Badge variant="outline">{r.status}</Badge>
                {isMaker && <span className="text-xs text-muted-foreground">prepared by you</span>}
                <span className="ml-auto text-xs text-muted-foreground">
                  expires {new Date(r.expires_at).toLocaleString()}
                </span>
              </div>
              <p className="text-sm">{r.reason}</p>
              <Payload request={r} />
              {r.decision_reason && (
                <p className="text-xs text-muted-foreground">Decision: {r.decision_reason}</p>
              )}
              {r.execution_ref && (
                <p className="text-xs text-muted-foreground">Executed as {r.execution_ref}</p>
              )}
              <div className="flex flex-wrap gap-2">
                {r.status === "draft" && isMaker && can("finance.prepare") && (
                  <Button size="sm" onClick={() => setAction({ request: r, kind: "submit" })}>
                    Confirm and submit
                  </Button>
                )}
                {r.status === "pending" && !isMaker && can("finance.approve") && (
                  <>
                    <Button size="sm" onClick={() => setAction({ request: r, kind: "approve" })}>
                      Approve
                    </Button>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => setAction({ request: r, kind: "reject" })}
                    >
                      Reject
                    </Button>
                  </>
                )}
                {(r.status === "draft" || r.status === "pending") && isMaker && (
                  <Button size="sm" variant="ghost" onClick={() => cancel(r)}>
                    Cancel
                  </Button>
                )}
              </div>
            </CardContent>
          </Card>
        );
      })}
      {action && (
        <ReauthDialog
          open
          onOpenChange={(open) => !open && setAction(null)}
          title={
            action.kind === "submit"
              ? "Submit for a second admin"
              : action.kind === "approve"
                ? "Approve and execute"
                : "Reject request"
          }
          description="Confirm your password for exactly this request. It cannot be changed afterwards."
          purpose={action.kind === "submit" ? APPROVAL_SUBMIT_PURPOSE : APPROVAL_DECIDE_PURPOSE}
          operationHash={action.request.payload_hash}
          confirmLabel={
            action.kind === "approve" ? "Approve" : action.kind === "reject" ? "Reject" : "Submit"
          }
          onProof={act}
        >
          {action.kind !== "submit" && (
            <div className="flex flex-col gap-1">
              <Label htmlFor="decision-reason">Reason</Label>
              <Input
                id="decision-reason"
                required
                maxLength={500}
                value={reason}
                onChange={(e) => setReason(e.target.value)}
              />
            </div>
          )}
        </ReauthDialog>
      )}
    </div>
  );
}
