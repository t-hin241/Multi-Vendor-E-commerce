"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { ReauthDialog } from "@/components/admin/reauth-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { needsReauthentication } from "@/lib/admin-access";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { formatMoney } from "@/lib/format";
import {
  ATTEMPT_DECIDE_PURPOSE,
  ATTEMPT_STAGE_LABELS,
  DESTINATION_DECIDE_PURPOSE,
  DESTINATION_REVEAL_PURPOSE,
  MANUAL_STAGE_LABELS,
  attemptActions,
  attemptDecisionRef,
  destinationDecisionRef,
  destinationRevealRef,
  isActiveAttempt,
} from "@/lib/manual-refunds";
import { queryKeys } from "@/lib/query-keys";

// A step that may need the admin's password (AF-19 step-up): it runs
// without a proof first and asks for the password only when Payment says
// so.
type StepUp = {
  title: string;
  purpose: string;
  operation: string;
  run: (proof?: string) => Promise<unknown>;
};

// ManualRefundPanel runs one refund through the manual bank transfer
// (AF-06): verify the buyer's destination, prepare and claim a transfer,
// read the account once, record the bank reference, and let a second
// admin confirm. Payment enforces every rule; this only offers the steps.
export function ManualRefundPanel({ refundId }: { refundId: string }) {
  const { callWithAuth, user } = useAuth();
  const queryClient = useQueryClient();
  const [stepUp, setStepUp] = useState<StepUp | null>(null);
  const [revealed, setRevealed] = useState<api.RevealedRefundDestination | null>(null);
  const [error, setError] = useState<string | null>(null);

  const detail = useQuery({
    queryKey: queryKeys.manualRefund(refundId),
    queryFn: () => callWithAuth((token) => api.getManualRefund(token, refundId)),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: queryKeys.manualRefund(refundId) });
    await queryClient.invalidateQueries({ queryKey: ["payment-refunds"] });
  }

  async function act(step: StepUp) {
    setError(null);
    try {
      await step.run(undefined);
      await refresh();
    } catch (err) {
      if (needsReauthentication(err)) {
        setStepUp(step);
        return;
      }
      setError(describeApiError(err, "Could not complete the step."));
      await refresh();
    }
  }

  async function simple(run: () => Promise<unknown>) {
    setError(null);
    try {
      await run();
    } catch (err) {
      setError(describeApiError(err, "Could not complete the step."));
    }
    await refresh();
  }

  if (detail.isPending) return <p className="text-sm text-muted-foreground">Loading transfer…</p>;
  if (detail.error || !detail.data) {
    return (
      <p className="text-sm text-destructive">
        {describeApiError(detail.error, "Could not load the manual transfer.")}
      </p>
    );
  }
  const d = detail.data;
  const current = d.destinations.find(
    (x) => x.status === "pending_verification" || x.status === "verified",
  );
  const active = d.attempts.find((a) => isActiveAttempt(a.stage));
  const open = d.refund.status === "awaiting_provider_refund" || d.refund.status === "pending";

  return (
    <div className="flex flex-col gap-3 rounded-md border bg-muted/30 p-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">Manual transfer:</span>
        <span>{MANUAL_STAGE_LABELS[d.stage] ?? d.stage}</span>
      </div>

      {d.destinations.length === 0 && (
        <p className="text-muted-foreground">
          The buyer has not given a destination yet. Never transfer to an account sent in a support
          message.
        </p>
      )}
      {d.destinations.map((dest) => (
        <p key={dest.id} className={dest.status === "superseded" ? "text-muted-foreground" : ""}>
          v{dest.version} · {dest.masked} · {dest.status.replace(/_/g, " ")}
          {dest.decision_reason && <span> · {dest.decision_reason}</span>}
        </p>
      ))}

      {open && current && (
        <div className="flex flex-wrap gap-2">
          {current.status === "pending_verification" && (
            <>
              <ReasonDialog
                variant="default"
                trigger={<Button size="sm">Verify destination</Button>}
                title={`Verify ${current.masked}?`}
                description="Check that the holder matches the buyer before verifying."
                confirmLabel="Verify"
                onConfirm={(reason) =>
                  act({
                    title: "Verify refund destination",
                    purpose: DESTINATION_DECIDE_PURPOSE,
                    operation: destinationDecisionRef(refundId, current.version, true),
                    run: (proof) =>
                      callWithAuth((token) =>
                        api.decideRefundDestination(token, refundId, {
                          destination_version: current.version,
                          decision: "verify",
                          reason,
                          proof,
                        }),
                      ),
                  })
                }
              />
              <ReasonDialog
                trigger={
                  <Button size="sm" variant="outline" className="text-destructive">
                    Reject
                  </Button>
                }
                title="Reject this destination?"
                description="The buyer sees the reason and must give another account."
                confirmLabel="Reject"
                onConfirm={(reason) =>
                  act({
                    title: "Reject refund destination",
                    purpose: DESTINATION_DECIDE_PURPOSE,
                    operation: destinationDecisionRef(refundId, current.version, false),
                    run: (proof) =>
                      callWithAuth((token) =>
                        api.decideRefundDestination(token, refundId, {
                          destination_version: current.version,
                          decision: "reject",
                          reason,
                          proof,
                        }),
                      ),
                  })
                }
              />
            </>
          )}
          <ReasonDialog
            variant="default"
            trigger={
              <Button size="sm" variant="outline">
                Show full account
              </Button>
            }
            title="Show the full account?"
            description="Only the admin making the transfer or a verifier may. Every access is audited."
            confirmLabel="Show"
            onConfirm={(reason) =>
              act({
                title: "Show the full refund account",
                purpose: DESTINATION_REVEAL_PURPOSE,
                operation: destinationRevealRef(refundId, current.version),
                run: (proof) =>
                  callWithAuth((token) =>
                    api.revealRefundDestination(token, refundId, { reason, proof }),
                  ).then(setRevealed),
              })
            }
          />
          {current.status === "verified" && !active && (
            <ReasonDialog
              variant="default"
              trigger={<Button size="sm">Prepare transfer</Button>}
              title={`Prepare a transfer of ${formatMoney(d.refund.amount, d.refund.currency)}?`}
              description={`To ${current.masked}. An operator then claims it and transfers outside the app.`}
              confirmLabel="Prepare"
              onConfirm={(reason) =>
                simple(() =>
                  callWithAuth((token) =>
                    api.prepareRefundAttempt(token, refundId, {
                      destination_version: current.version,
                      reason,
                    }),
                  ),
                )
              }
            />
          )}
        </div>
      )}

      {revealed && (
        <div className="flex flex-col gap-1 rounded-md border border-dashed p-2">
          <p>
            v{revealed.destination_version} · {revealed.bank_code} · {revealed.account_number} ·{" "}
            {revealed.account_name}
          </p>
          <Button
            size="sm"
            variant="ghost"
            className="self-start"
            onClick={() => setRevealed(null)}
          >
            Hide
          </Button>
        </div>
      )}

      {active && (
        <AttemptSteps
          attempt={active}
          me={user?.id}
          onSimple={simple}
          onStepUp={act}
          callWithAuth={callWithAuth}
        />
      )}

      {d.attempts.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="font-medium">Attempts</span>
          {d.attempts.map((a) => (
            <div key={a.id} className="text-xs text-muted-foreground">
              {new Date(a.created_at).toLocaleString()} · {ATTEMPT_STAGE_LABELS[a.stage]} · v
              {a.destination_version}
              {a.bank_reference && ` · ref ${a.bank_reference} from ${a.source_account}`}
              {a.decision_reason && ` · ${a.decision_reason}`}
              {a.evidence.map((e) => (
                <EvidenceLink key={e.id} evidence={e} />
              ))}
            </div>
          ))}
        </div>
      )}

      {error && <p className="text-destructive">{error}</p>}

      {stepUp && (
        <ReauthDialog
          open
          onOpenChange={(next) => !next && setStepUp(null)}
          title={stepUp.title}
          description="Confirm your password for this step. It is audited."
          purpose={stepUp.purpose}
          operationHash={stepUp.operation}
          onProof={async (proof) => {
            await stepUp.run(proof);
            setStepUp(null);
            await refresh();
          }}
        />
      )}
    </div>
  );
}

function AttemptSteps({
  attempt,
  me,
  onSimple,
  onStepUp,
  callWithAuth,
}: {
  attempt: api.RefundAttempt;
  me: string | undefined;
  onSimple: (run: () => Promise<unknown>) => Promise<void>;
  onStepUp: (step: StepUp) => Promise<void>;
  callWithAuth: <R>(fn: (token: string) => Promise<R>) => Promise<R>;
}) {
  const can = attemptActions(attempt, me);
  const decide = (confirm: boolean) => (reason: string) =>
    onStepUp({
      title: confirm ? "Confirm the transfer" : "Mark the transfer failed",
      purpose: ATTEMPT_DECIDE_PURPOSE,
      operation: attemptDecisionRef(attempt.id, attempt.version, confirm),
      run: (proof) =>
        callWithAuth((token) =>
          api.decideRefundAttempt(token, attempt.id, {
            expected_version: attempt.version,
            decision: confirm ? "confirm" : "fail",
            reason,
            proof,
          }),
        ),
    });

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <p>
        Transfer {formatMoney(attempt.amount, attempt.currency)} ·{" "}
        {ATTEMPT_STAGE_LABELS[attempt.stage]}
        {attempt.claimed_by &&
          ` · claimed by ${attempt.claimed_by === me ? "you" : attempt.claimed_by.slice(0, 8)}`}
        {attempt.stage === "executing" &&
          attempt.lease_expires_at &&
          ` until ${new Date(attempt.lease_expires_at).toLocaleTimeString()}`}
      </p>
      {attempt.stage === "unknown" && (
        <p className="text-destructive">
          The claim ran out without a reference. Check the bank statement: record the transfer if it
          left, or mark it failed. Do not transfer again before that.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        {can.claim && (
          <Button
            size="sm"
            onClick={() =>
              onSimple(() =>
                callWithAuth((token) => api.claimRefundAttempt(token, attempt.id, attempt.version)),
              )
            }
          >
            Claim transfer
          </Button>
        )}
        {can.cancel && (
          <ReasonDialog
            trigger={
              <Button size="sm" variant="outline">
                {attempt.stage === "executing" ? "Release (nothing sent)" : "Void"}
              </Button>
            }
            title={attempt.stage === "executing" ? "Release this claim?" : "Void this transfer?"}
            description={
              attempt.stage === "executing"
                ? "Only if no money was sent. If you are not sure, let the claim run out instead."
                : "Nothing has been sent yet."
            }
            confirmLabel="Confirm"
            onConfirm={(reason) =>
              onSimple(() =>
                callWithAuth((token) =>
                  api.cancelRefundAttempt(token, attempt.id, {
                    expected_version: attempt.version,
                    reason,
                  }),
                ),
              )
            }
          />
        )}
        {can.confirm && (
          <ReasonDialog
            variant="default"
            trigger={<Button size="sm">Confirm transfer</Button>}
            title="Confirm the money left?"
            description={`Reference ${attempt.bank_reference ?? ""} from ${attempt.source_account ?? ""}. Check it on the statement first.`}
            confirmLabel="Confirm"
            onConfirm={decide(true)}
          />
        )}
        {can.decide && (
          <ReasonDialog
            trigger={
              <Button size="sm" variant="outline" className="text-destructive">
                Mark failed
              </Button>
            }
            title="Mark this transfer failed?"
            description="Only when the statement shows no transfer or it bounced. The refund stays open."
            confirmLabel="Mark failed"
            onConfirm={decide(false)}
          />
        )}
      </div>
      {can.submit && (
        <SubmitTransferForm attempt={attempt} onSimple={onSimple} callWithAuth={callWithAuth} />
      )}
    </div>
  );
}

function SubmitTransferForm({
  attempt,
  onSimple,
  callWithAuth,
}: {
  attempt: api.RefundAttempt;
  onSimple: (run: () => Promise<unknown>) => Promise<void>;
  callWithAuth: <R>(fn: (token: string) => Promise<R>) => Promise<R>;
}) {
  const [reference, setReference] = useState("");
  const [source, setSource] = useState("");
  const [executedAt, setExecutedAt] = useState("");
  const [evidence, setEvidence] = useState<api.RefundEvidence[]>([]);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function upload(file: File) {
    setUploadError(null);
    try {
      const item = await callWithAuth((token) => api.uploadRefundEvidence(token, attempt.id, file));
      setEvidence((list) => [...list, item]);
    } catch (err) {
      setUploadError(describeApiError(err, "Could not upload the receipt."));
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    await onSimple(() =>
      callWithAuth((token) =>
        api.submitRefundAttempt(token, attempt.id, {
          expected_version: attempt.version,
          bank_reference: reference.trim(),
          source_account: source.trim(),
          executed_at: new Date(executedAt).toISOString(),
          evidence_ids: evidence.map((x) => x.id),
        }),
      ),
    );
    setBusy(false);
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-2">
      <p className="text-xs text-muted-foreground">
        After the bank shows the transfer as sent, record its reference. A second admin confirms.
      </p>
      <Label htmlFor={`ref-${attempt.id}`}>Bank reference</Label>
      <Input
        id={`ref-${attempt.id}`}
        maxLength={100}
        value={reference}
        onChange={(e) => setReference(e.target.value)}
      />
      <Label htmlFor={`src-${attempt.id}`}>Platform account it left from</Label>
      <Input
        id={`src-${attempt.id}`}
        maxLength={64}
        value={source}
        onChange={(e) => setSource(e.target.value)}
      />
      <Label htmlFor={`at-${attempt.id}`}>Executed at</Label>
      <Input
        id={`at-${attempt.id}`}
        type="datetime-local"
        value={executedAt}
        onChange={(e) => setExecutedAt(e.target.value)}
      />
      <Label htmlFor={`file-${attempt.id}`}>Receipt (JPEG, PNG or PDF, optional)</Label>
      <Input
        id={`file-${attempt.id}`}
        type="file"
        accept="image/jpeg,image/png,application/pdf"
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) void upload(file);
          e.target.value = "";
        }}
      />
      {evidence.length > 0 && (
        <p className="text-xs text-muted-foreground">{evidence.length} receipt(s) uploaded</p>
      )}
      {uploadError && <p className="text-xs text-destructive">{uploadError}</p>}
      <Button
        type="submit"
        size="sm"
        className="self-start"
        disabled={busy || reference.trim().length < 3 || source.trim().length < 2 || !executedAt}
      >
        {busy ? "Working…" : "Record transfer"}
      </Button>
    </form>
  );
}

function EvidenceLink({ evidence }: { evidence: api.RefundEvidence }) {
  const { callWithAuth } = useAuth();
  async function open() {
    const blob = await callWithAuth((token) => api.fetchRefundEvidence(token, evidence.id));
    const url = URL.createObjectURL(blob);
    window.open(url, "_blank", "noopener");
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  }
  return (
    <button type="button" className="ml-2 underline" onClick={() => void open()}>
      receipt
    </button>
  );
}
