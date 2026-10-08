"use client";

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { SectionHeader } from "@/components/section-header";
import { SupportStatusBadge } from "@/components/support/support-status-badge";
import { SupportThread } from "@/components/support/support-thread";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";
import {
  useSupportCapability,
  useSupportCase,
  useSupportCaseRefresh,
} from "@/lib/hooks/use-support-cases";
import { returnStatusLabel } from "@/lib/order-workflow";
import {
  CASE_HOLD_LABELS,
  adminCanResolve,
  caseRefundBlocked,
  isOverdue,
  supportCategoryLabel,
  supportResolutionLabel,
} from "@/lib/support-cases";

// SupportCaseConsole is where an admin works a case: pick it up, ask the
// buyer or the shop, keep internal notes, and record the conclusion. A
// conclusion that needs money or goods links a refund or return created
// from the order page; the case resolves only once that outcome is
// confirmed. Every action sends the version the admin saw, so two admins
// cannot overwrite each other.
export function SupportCaseConsole({ caseId }: { caseId: string }) {
  const { callWithAuth, user } = useAuth();
  const caseQuery = useSupportCase("admin", caseId, true);
  const capability = useSupportCapability(true, "admin");
  const refresh = useSupportCaseRefresh("admin", caseId);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  if (caseQuery.isPending) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (caseQuery.error || !caseQuery.data) {
    return <ActionError error={caseQuery.error ?? new Error("Case not found.")} />;
  }
  const c = caseQuery.data;

  async function act(fn: (token: string) => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await callWithAuth(fn);
      await refresh();
    } catch (err) {
      setError(err);
      if (err instanceof api.ApiError && err.code === "version_conflict") await refresh();
      throw err;
    } finally {
      setBusy(false);
    }
  }
  const run = (fn: (token: string) => Promise<unknown>) => act(fn).catch(() => undefined);

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title={`Case ${c.id.slice(0, 8)} · ${supportCategoryLabel(c.category)}`}
        badge={<SupportStatusBadge status={c.status} locale="en" />}
        subtitle={
          <>
            <Link className="text-primary underline" href={`/admin/orders/${c.order_id}`}>
              Order {c.order_id.slice(0, 8)}
            </Link>{" "}
            · vendor order {c.vendor_order_id.slice(0, 8)} · vendor {c.vendor_id.slice(0, 8)} ·
            buyer {c.buyer_id?.slice(0, 8)} · v{c.version}
          </>
        }
        action={
          <Link href="/admin/support" className="text-sm text-primary underline">
            Back to queue
          </Link>
        }
      />

      <div className="grid gap-4 lg:grid-cols-[1fr_22rem]">
        <SupportThread
          scope="admin"
          detail={c}
          canReply={c.status !== "closed"}
          locale="en"
          capability={capability.data}
          onChanged={refresh}
        />

        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Handling</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-2 text-sm">
              <p>
                Assignee:{" "}
                {c.assignee_id ? (
                  <span className="font-mono">
                    {c.assignee_id === user?.id ? "you" : c.assignee_id.slice(0, 8)}
                  </span>
                ) : (
                  "nobody"
                )}
              </p>
              <p className={isOverdue(c) ? "text-destructive" : undefined}>
                Due: {c.due_at ? new Date(c.due_at).toLocaleString("vi-VN") : "—"}
              </p>
              {c.settlement_hold ? (
                <p
                  className={
                    c.settlement_hold.status === "needs_review"
                      ? "text-destructive"
                      : "text-muted-foreground"
                  }
                >
                  {CASE_HOLD_LABELS[c.settlement_hold.status] ?? c.settlement_hold.status}
                  {c.settlement_hold.note && `: ${c.settlement_hold.note}`}
                </p>
              ) : (
                c.financial_hold &&
                c.status !== "closed" && (
                  <p className="text-muted-foreground">
                    The vendor order&apos;s payout is held until the case is closed.
                  </p>
                )
              )}
              {c.status !== "resolved" &&
                c.status !== "closed" &&
                (c.assignee_id !== user?.id || c.status === "open") && (
                  <ReasonDialog
                    title="Nhận xử lý hồ sơ"
                    confirmLabel="Nhận việc"
                    trigger={
                      <Button size="sm" disabled={busy || !user}>
                        {c.status === "open" ? "Start handling" : "Take over"}
                      </Button>
                    }
                    onConfirm={(reason) =>
                      run((t) => api.assignSupportCase(t, c.id, user!.id, c.version, reason))
                    }
                  />
                )}
              {c.status !== "open" && adminCanResolve(c) && (
                <StatusActions c={c} busy={busy} run={run} />
              )}
              <ActionError error={error} />
            </CardContent>
          </Card>

          {adminCanResolve(c) && <ResolveCard c={c} busy={busy} act={act} />}

          {(c.status === "resolution_pending" ||
            c.status === "resolved" ||
            c.status === "closed") &&
            c.resolution_kind && (
              <Card>
                <CardHeader>
                  <CardTitle className="text-base">Resolution</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-col gap-2 text-sm">
                  <p>{supportResolutionLabel(c.resolution_kind)}</p>
                  {c.resolution_ref && (
                    <p className="font-mono text-xs">linked {c.resolution_ref.slice(0, 8)}</p>
                  )}
                  {c.resolution_note && (
                    <p className="whitespace-pre-wrap text-muted-foreground">
                      Shown to the buyer: {c.resolution_note}
                    </p>
                  )}
                  {c.status === "resolution_pending" && (
                    <p className="text-muted-foreground">
                      Waiting for the refund/return outcome. A failure puts the case back in
                      progress.
                    </p>
                  )}
                  {c.status === "resolved" && (
                    <ReasonDialog
                      trigger={
                        <Button size="sm" variant="outline" disabled={busy}>
                          Close case
                        </Button>
                      }
                      title="Close this case?"
                      description="The buyer can no longer reopen it; the payout hold is released."
                      confirmLabel="Close"
                      variant="default"
                      onConfirm={(reason) =>
                        act((t) => api.closeSupportCase(t, c.id, reason, c.version))
                      }
                    />
                  )}
                </CardContent>
              </Card>
            )}
        </div>
      </div>
    </div>
  );
}

function StatusActions({
  c,
  busy,
  run,
}: {
  c: api.SupportCase;
  busy: boolean;
  run: (fn: (token: string) => Promise<unknown>) => Promise<unknown>;
}) {
  const [note, setNote] = useState("");
  const move = (status: "in_progress" | "waiting_buyer" | "waiting_vendor") =>
    run((t) =>
      api.changeSupportCaseStatus(t, c.id, { status, note: note.trim() || undefined }, c.version),
    ).then(() => setNote(""));
  return (
    <div className="flex flex-col gap-2 border-t pt-2">
      <Input
        value={note}
        maxLength={1000}
        placeholder="Internal note for the timeline (optional)"
        onChange={(e) => setNote(e.target.value)}
      />
      <div className="flex flex-wrap gap-2">
        {c.status !== "waiting_buyer" && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => move("waiting_buyer")}>
            Ask buyer
          </Button>
        )}
        {c.status !== "waiting_vendor" && (
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => move("waiting_vendor")}
          >
            Ask vendor
          </Button>
        )}
        {c.status !== "in_progress" && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => move("in_progress")}>
            Back in progress
          </Button>
        )}
      </div>
      <p className="text-xs text-muted-foreground">
        Post the question as a public message first; a reply puts the case back in progress.
      </p>
    </div>
  );
}

// ResolveCard records the conclusion. A dispute refund can be opened here
// (PW-014: refund and link in one step, once the payout hold is
// confirmed) or an existing refund/return linked; returns are requested
// by the buyer.
function ResolveCard({
  c,
  busy,
  act,
}: {
  c: api.SupportCaseDetail;
  busy: boolean;
  act: (fn: (token: string) => Promise<unknown>) => Promise<void>;
}) {
  const { callWithAuth } = useAuth();
  const [kind, setKind] = useState<api.SupportResolutionKind>("no_action");
  const [linked, setLinked] = useState("");
  const [reason, setReason] = useState("");
  const orderQuery = useQuery({
    queryKey: ["admin-order", c.order_id],
    queryFn: () => callWithAuth((token) => api.getAdminOrder(token, c.order_id)),
    enabled: kind !== "no_action",
  });
  const order = orderQuery.data;
  const itemVendorOrder = new Map((order?.items ?? []).map((i) => [i.id, i.vendor_order_id]));
  const refunds = (order?.refunds ?? []).filter(
    (r) =>
      (!r.vendor_order_id || r.vendor_order_id === c.vendor_order_id) &&
      (r.status === "requested" || r.status === "submitted" || r.status === "succeeded"),
  );
  const returns = (order?.returns ?? []).filter(
    (r) => itemVendorOrder.get(r.order_item_id) === c.vendor_order_id && r.status !== "rejected",
  );
  const valid = reason.trim() && (kind === "no_action" || linked);

  async function submit() {
    try {
      await act((t) =>
        api.resolveSupportCase(
          t,
          c.id,
          { kind, linkedOperationId: linked, reason: reason.trim() },
          c.version,
        ),
      );
      setReason("");
      setLinked("");
    } catch {
      // The error is shown in the Handling card; keep the draft.
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Resolve</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <div className="flex flex-col gap-1">
          <Label>Outcome</Label>
          <Select
            value={kind}
            onValueChange={(v) => {
              setKind(v as api.SupportResolutionKind);
              setLinked("");
            }}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="no_action">No money or goods involved</SelectItem>
              <SelectItem value="refund">Refund (link an existing refund)</SelectItem>
              <SelectItem value="return">Return (link the buyer&apos;s return)</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {kind !== "no_action" && (
          <div className="flex flex-col gap-1">
            <Label>Linked {kind}</Label>
            {orderQuery.isPending && <p className="text-muted-foreground">Loading the order…</p>}
            {kind === "refund" && (
              <>
                {refunds.map((r) => (
                  <label key={r.id} className="flex items-center gap-2">
                    <input
                      type="radio"
                      name="linked"
                      checked={linked === r.id}
                      onChange={() => setLinked(r.id)}
                    />
                    {formatMoney(r.amount, r.currency)} · {r.reason_code} · {r.status}
                  </label>
                ))}
                {order && refunds.length === 0 && (
                  <p className="text-muted-foreground">No refund for this vendor order yet.</p>
                )}
                <CaseRefundForm
                  c={c}
                  currency={order?.currency}
                  reason={reason}
                  busy={busy}
                  act={act}
                  onDone={() => setReason("")}
                />
              </>
            )}
            {kind === "return" && (
              <>
                {returns.map((r) => (
                  <label key={r.id} className="flex items-center gap-2">
                    <input
                      type="radio"
                      name="linked"
                      checked={linked === r.id}
                      onChange={() => setLinked(r.id)}
                    />
                    {r.quantity} unit(s) · {formatMoney(r.refund_amount, order?.currency)} ·{" "}
                    {returnStatusLabel(r.status)}
                  </label>
                ))}
                {order && returns.length === 0 && (
                  <p className="text-muted-foreground">
                    No return for this vendor order. Ask the buyer to request one from the order.
                  </p>
                )}
              </>
            )}
          </div>
        )}
        <div className="flex flex-col gap-1">
          <Label htmlFor="resolution-reason">Conclusion (shown to the buyer and the shop)</Label>
          <Textarea
            id="resolution-reason"
            rows={3}
            maxLength={500}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>
        <Button disabled={!valid || busy} onClick={submit}>
          {kind === "no_action" ? "Resolve" : "Propose and wait for the outcome"}
        </Button>
      </CardContent>
    </Card>
  );
}

// CaseRefundForm opens a dispute refund on the case's vendor order and
// links it, using the conclusion above as the refund reason. The key is
// kept until it succeeds, so a retry after a timeout cannot open two.
function CaseRefundForm({
  c,
  currency,
  reason,
  busy,
  act,
  onDone,
}: {
  c: api.SupportCaseDetail;
  currency?: string;
  reason: string;
  busy: boolean;
  act: (fn: (token: string) => Promise<unknown>) => Promise<void>;
  onDone: () => void;
}) {
  const [amount, setAmount] = useState("");
  const [key, setKey] = useState(() => crypto.randomUUID());
  const blocked = caseRefundBlocked(c, c.settlement_hold);
  const value = Number(amount);
  const valid = Number.isInteger(value) && value > 0 && reason.trim().length > 0;

  async function submit() {
    try {
      await act((t) =>
        api.createCaseRefund(
          t,
          c.id,
          { amount: value, reason: reason.trim(), expected_version: c.version },
          key,
        ),
      );
      setAmount("");
      setKey(crypto.randomUUID());
      onDone();
    } catch {
      // Shown in the Handling card; the same key is reused on retry.
    }
  }

  return (
    <div className="mt-2 flex flex-col gap-1 border-t pt-2">
      <Label htmlFor="case-refund-amount">
        Or open a dispute refund from this case{currency ? ` (${currency}, minor units)` : ""}
      </Label>
      <Input
        id="case-refund-amount"
        inputMode="numeric"
        value={amount}
        onChange={(e) => setAmount(e.target.value.replace(/[^0-9]/g, ""))}
      />
      {blocked && <p className="text-xs text-muted-foreground">{blocked}</p>}
      <Button
        size="sm"
        variant="outline"
        disabled={!valid || busy || Boolean(blocked)}
        onClick={submit}
      >
        {valid && currency
          ? `Refund ${formatMoney(value, currency)} and link it`
          : "Refund and link it"}
      </Button>
    </div>
  );
}
