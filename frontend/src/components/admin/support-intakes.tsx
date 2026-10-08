"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ReasonDialog } from "@/components/admin/confirm-dialogs";
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
import { SUPPORT_CATEGORIES } from "@/lib/support-cases";

const REFERENCE_KIND_LABELS: Record<api.SupportIntakeReferenceKind, string> = {
  bank_transfer: "bank transfer",
  payment: "payment",
  checkout: "checkout",
};

// SupportIntakes is the queue of buyer requests without an order id
// (PW-012). The admin finds the order from the buyer's reference (payment
// search) and links it; Order refuses an order of another buyer. A request
// that matches nothing is closed with a reason the buyer reads.
export function SupportIntakes() {
  const { callWithAuth } = useAuth();
  const intakes = useQuery({
    queryKey: ["support-intakes", "open"],
    queryFn: () => callWithAuth((token) => api.listSupportIntakes(token, "open")),
  });
  if (intakes.isPending || !intakes.data?.length) {
    return intakes.error ? <ActionError error={intakes.error} /> : null;
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">
          Requests without an order ({intakes.data.length})
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 text-sm">
        {intakes.data.map((intake) => (
          <IntakeRow key={intake.id} intake={intake} />
        ))}
      </CardContent>
    </Card>
  );
}

function IntakeRow({ intake }: { intake: api.SupportIntake }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [linking, setLinking] = useState(false);
  const [error, setError] = useState<unknown>(null);

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["support-intakes"] });
    await queryClient.invalidateQueries({ queryKey: ["support-cases"] });
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span>
          <span className="font-medium">{REFERENCE_KIND_LABELS[intake.reference_kind]}</span>{" "}
          <span className="font-mono">{intake.reference}</span> · buyer{" "}
          {intake.buyer_id?.slice(0, 8)} · {new Date(intake.created_at).toLocaleString("vi-VN")}
        </span>
        <div className="flex gap-2">
          <Button size="sm" onClick={() => setLinking((v) => !v)}>
            {linking ? "Cancel" : "Link to order"}
          </Button>
          <ReasonDialog
            trigger={
              <Button size="sm" variant="outline">
                Close
              </Button>
            }
            title="Close this request?"
            description="Use it when no order of this buyer matches. The buyer reads the reason."
            confirmLabel="Close request"
            onConfirm={async (reason) => {
              await callWithAuth((t) =>
                api.closeSupportIntake(t, intake.id, { expected_version: intake.version, reason }),
              );
              await refresh();
            }}
          />
        </div>
      </div>
      <p className="whitespace-pre-wrap text-muted-foreground">{intake.message}</p>
      {linking && (
        <LinkForm
          intake={intake}
          onLinked={async () => {
            setLinking(false);
            await refresh();
          }}
          onError={setError}
        />
      )}
      <ActionError error={error} />
    </div>
  );
}

function LinkForm({
  intake,
  onLinked,
  onError,
}: {
  intake: api.SupportIntake;
  onLinked: () => Promise<void>;
  onError: (err: unknown) => void;
}) {
  const { callWithAuth } = useAuth();
  const [orderId, setOrderId] = useState("");
  const [vendorOrderId, setVendorOrderId] = useState("");
  const [category, setCategory] = useState<api.SupportCategory>("payment_issue");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const search = useQuery({
    queryKey: ["payment-search", intake.reference],
    queryFn: () => callWithAuth((token) => api.searchPayments(token, intake.reference)),
    enabled: intake.reference.length >= 3,
    retry: false,
  });
  const validOrder = /^[0-9a-f-]{36}$/i.test(orderId.trim());
  const order = useQuery({
    queryKey: ["admin-order", orderId.trim()],
    queryFn: () => callWithAuth((token) => api.getAdminOrder(token, orderId.trim())),
    enabled: validOrder,
    retry: false,
  });
  // Only payments of this buyer are offered; Order checks it again.
  const candidates = (search.data?.intents ?? []).filter((p) => p.buyer_id === intake.buyer_id);

  async function link() {
    setBusy(true);
    onError(null);
    try {
      await callWithAuth((t) =>
        api.linkSupportIntake(t, intake.id, {
          order_id: orderId.trim(),
          vendor_order_id: vendorOrderId,
          category,
          expected_version: intake.version,
          reason: reason.trim(),
        }),
      );
      await onLinked();
    } catch (err) {
      onError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-2 border-t pt-2">
      <p className="text-xs text-muted-foreground">
        Payments of this buyer matching the reference:{" "}
        {search.error
          ? "payment search unavailable (needs finance.read); enter the order id"
          : search.isPending
            ? "searching…"
            : candidates.length === 0
              ? "none found"
              : ""}
      </p>
      {candidates.map((p) => (
        <button
          key={p.id}
          type="button"
          className="text-left underline"
          onClick={() => setOrderId(p.order_id)}
        >
          Order {p.order_id.slice(0, 8)} · {formatMoney(p.amount, p.currency)} · {p.status}
        </button>
      ))}
      <Label htmlFor={`intake-order-${intake.id}`}>Order id</Label>
      <Input
        id={`intake-order-${intake.id}`}
        value={orderId}
        onChange={(e) => {
          setOrderId(e.target.value);
          setVendorOrderId("");
        }}
      />
      {validOrder && order.data && (
        <>
          <Link className="text-primary underline" href={`/admin/orders/${order.data.id}`}>
            Open order {order.data.id.slice(0, 8)}
          </Link>
          <Label>Shop package</Label>
          {(order.data.vendor_orders ?? []).map((vo) => (
            <label key={vo.id} className="flex items-center gap-2">
              <input
                type="radio"
                name={`intake-vo-${intake.id}`}
                checked={vendorOrderId === vo.id}
                onChange={() => setVendorOrderId(vo.id)}
              />
              {vo.id.slice(0, 8)} · vendor {vo.vendor_id?.slice(0, 8)} ·{" "}
              {formatMoney(vo.subtotal_amount, vo.currency)} · {vo.status}
            </label>
          ))}
        </>
      )}
      {validOrder && order.error && <ActionError error={order.error} />}
      <Label>Topic</Label>
      <Select value={category} onValueChange={(v) => setCategory(v as api.SupportCategory)}>
        <SelectTrigger>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {SUPPORT_CATEGORIES.map((cat) => (
            <SelectItem key={cat.value} value={cat.value}>
              {cat.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Label htmlFor={`intake-reason-${intake.id}`}>How you verified it (audit)</Label>
      <Textarea
        id={`intake-reason-${intake.id}`}
        rows={2}
        maxLength={500}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      />
      <Button
        size="sm"
        className="self-start"
        disabled={busy || !validOrder || !vendorOrderId || !reason.trim()}
        onClick={link}
      >
        {busy ? "Linking…" : "Link and open the case"}
      </Button>
    </div>
  );
}
