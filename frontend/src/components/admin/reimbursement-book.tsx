"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { ReauthDialog } from "@/components/admin/reauth-dialog";
import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { needsReauthentication } from "@/lib/admin-access";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";

const STATUS_OPTIONS = ["requested", "approved", "paid", "rejected", ""] as const;

const REASONS: { value: api.ReimbursementReason; label: string }[] = [
  { value: "return_shipping_fee", label: "Return shipping fee" },
  { value: "goodwill", label: "Goodwill" },
  { value: "other", label: "Other" },
];

// ReimbursementBook is the marketplace's own disbursements to buyers
// outside a capture (PW-032): one admin prepares, another approves, then
// the transfer to the buyer's verified refund account is recorded. It is
// never a refund and never charged to a shop.
export function ReimbursementBook() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("requested");
  const [error, setError] = useState<unknown>(null);
  const [stepUp, setStepUp] = useState<{
    id: string;
    version: number;
    approve: boolean;
    reason: string;
  } | null>(null);
  const list = useQuery({
    queryKey: ["reimbursements", status],
    queryFn: () => callWithAuth((t) => api.listReimbursements(t, status)),
    retry: false,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["reimbursements"] });

  async function decide(
    id: string,
    version: number,
    approve: boolean,
    reason: string,
    proof?: string,
  ) {
    await callWithAuth((t) =>
      api.decideReimbursement(t, id, {
        decision: approve ? "approve" : "reject",
        reason,
        expected_version: version,
        proof,
      }),
    );
    await refresh();
  }

  if (list.error instanceof api.ApiError && list.error.status === 404) {
    return null; // the feature is off
  }
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Reimbursements"
        subtitle="Money the marketplace pays a buyer outside a payment (e.g. a return shipping fee). Two admins; paid to the buyer's verified refund account."
        action={<StatusFilter status={status} onChange={setStatus} options={STATUS_OPTIONS} />}
      />
      <NewReimbursement onDone={refresh} />
      <Card>
        <CardContent className="flex flex-col gap-3 pt-6 text-sm">
          {list.data?.length === 0 && <p className="text-muted-foreground">None.</p>}
          {list.data?.map((r) => (
            <div key={r.id} className="flex flex-col gap-1 rounded-md border p-3">
              <p className="font-medium">
                {formatMoney(r.amount, r.currency)} · {r.reason_code.replace(/_/g, " ")} ·{" "}
                {r.status}
              </p>
              <p className="text-muted-foreground">
                Order #{r.order_id.slice(0, 8)} — {r.reason}
              </p>
              {r.decision_reason && (
                <p className="text-muted-foreground">Decision: {r.decision_reason}</p>
              )}
              {r.destination_masked && (
                <p>
                  Paid to {r.destination_masked}
                  {r.bank_reference && `, bank ref ${r.bank_reference}`}
                </p>
              )}
              <div className="flex flex-wrap gap-2">
                {r.status === "requested" && (
                  <>
                    <ReasonDialog
                      variant="default"
                      trigger={<Button size="sm">Approve</Button>}
                      title="Approve this reimbursement?"
                      description="You must not be the admin who prepared it."
                      confirmLabel="Approve"
                      onConfirm={async (reason) => {
                        try {
                          await decide(r.id, r.version, true, reason);
                        } catch (err) {
                          if (needsReauthentication(err)) {
                            setStepUp({ id: r.id, version: r.version, approve: true, reason });
                            return;
                          }
                          throw err;
                        }
                      }}
                    />
                    <ReasonDialog
                      trigger={
                        <Button size="sm" variant="outline" className="text-destructive">
                          Reject
                        </Button>
                      }
                      title="Reject this reimbursement?"
                      confirmLabel="Reject"
                      onConfirm={async (reason) => {
                        try {
                          await decide(r.id, r.version, false, reason);
                        } catch (err) {
                          if (needsReauthentication(err)) {
                            setStepUp({ id: r.id, version: r.version, approve: false, reason });
                            return;
                          }
                          throw err;
                        }
                      }}
                    />
                  </>
                )}
                {r.status === "approved" && (
                  <RecordPayment reimbursement={r} onDone={refresh} onError={setError} />
                )}
              </div>
            </div>
          ))}
          <ActionError error={error} />
        </CardContent>
      </Card>
      {stepUp && (
        <ReauthDialog
          open
          onOpenChange={(next) => !next && setStepUp(null)}
          title={stepUp.approve ? "Approve the reimbursement" : "Reject the reimbursement"}
          description="Confirm your password for this step. It is audited."
          purpose={api.REIMBURSEMENT_DECIDE_PURPOSE}
          operationHash={api.reimbursementDecisionRef(stepUp.id, stepUp.version, stepUp.approve)}
          onProof={async (proof) => {
            await decide(stepUp.id, stepUp.version, stepUp.approve, stepUp.reason, proof);
            setStepUp(null);
          }}
        />
      )}
    </div>
  );
}

function NewReimbursement({ onDone }: { onDone: () => Promise<void> }) {
  const { callWithAuth } = useAuth();
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState({
    order_id: "",
    reason_code: "return_shipping_fee",
    reason: "",
    amount: "",
  });
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [error, setError] = useState<unknown>(null);
  if (!open) {
    return (
      <Button variant="outline" className="self-start" onClick={() => setOpen(true)}>
        Prepare a reimbursement
      </Button>
    );
  }
  return (
    <Card>
      <CardContent className="pt-6">
        <form
          className="flex flex-col gap-2 text-sm"
          onSubmit={async (e) => {
            e.preventDefault();
            setError(null);
            try {
              await callWithAuth((t) =>
                api.createReimbursement(
                  t,
                  {
                    order_id: form.order_id.trim(),
                    reason_code: form.reason_code as api.ReimbursementReason,
                    reason: form.reason.trim(),
                    amount: Number(form.amount),
                    currency: "VND",
                  },
                  key,
                ),
              );
              setForm({ order_id: "", reason_code: "return_shipping_fee", reason: "", amount: "" });
              setKey(crypto.randomUUID());
              setOpen(false);
              await onDone();
            } catch (err) {
              setError(err);
            }
          }}
        >
          <Label htmlFor="rb-order">Order id</Label>
          <Input
            id="rb-order"
            value={form.order_id}
            onChange={(e) => setForm({ ...form, order_id: e.target.value })}
          />
          <Label htmlFor="rb-reason-code">Reason</Label>
          <select
            id="rb-reason-code"
            className="h-9 rounded-md border bg-background px-2"
            value={form.reason_code}
            onChange={(e) => setForm({ ...form, reason_code: e.target.value })}
          >
            {REASONS.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
          <Label htmlFor="rb-note">Details (with the receipt reference)</Label>
          <Input
            id="rb-note"
            maxLength={500}
            value={form.reason}
            onChange={(e) => setForm({ ...form, reason: e.target.value })}
          />
          <Label htmlFor="rb-amount">Amount (VND)</Label>
          <Input
            id="rb-amount"
            inputMode="numeric"
            value={form.amount}
            onChange={(e) => setForm({ ...form, amount: e.target.value.replace(/[^0-9]/g, "") })}
          />
          <div className="flex gap-2">
            <Button
              type="submit"
              size="sm"
              disabled={!form.order_id || !form.reason.trim() || !Number(form.amount)}
            >
              Prepare
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
          </div>
          <ActionError error={error} />
        </form>
      </CardContent>
    </Card>
  );
}

function RecordPayment({
  reimbursement: r,
  onDone,
  onError,
}: {
  reimbursement: api.Reimbursement;
  onDone: () => Promise<void>;
  onError: (err: unknown) => void;
}) {
  const { callWithAuth } = useAuth();
  const [reference, setReference] = useState("");
  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={async (e) => {
        e.preventDefault();
        onError(null);
        try {
          await callWithAuth((t) =>
            api.payReimbursement(t, r.id, {
              bank_reference: reference.trim(),
              expected_version: r.version,
            }),
          );
          await onDone();
        } catch (err) {
          onError(err);
        }
      }}
    >
      <label className="flex flex-col gap-1 text-xs">
        Bank reference of the transfer
        <Input
          className="h-8 w-56"
          maxLength={100}
          value={reference}
          onChange={(e) => setReference(e.target.value)}
        />
      </label>
      <Button type="submit" size="sm" disabled={reference.trim().length < 3}>
        Record transfer
      </Button>
    </form>
  );
}
