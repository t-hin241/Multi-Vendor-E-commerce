"use client";

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { BuyerRefund } from "@/lib/api-client";
import { formatMoney } from "@/lib/format";
import {
  refundDestinationError,
  useMyRefunds,
  useSubmitRefundDestination,
} from "@/lib/hooks/use-payments";
import {
  BUYER_REFUND_EVENT_LABELS,
  BUYER_REFUND_STAGE_LABELS,
  checkBeneficiary,
} from "@/lib/manual-refunds";

// RefundStatusPanel shows the buyer each refund of the order and, when the
// platform pays it by bank transfer (AF-06), asks where to send it. Only
// the masked account is ever shown back; nothing is promised before the
// transfer is confirmed.
export function RefundStatusPanel({ orderId }: { orderId: string }) {
  const refunds = useMyRefunds(orderId, true);
  if (!refunds.data?.length) return null;
  return (
    <Card className="mt-4">
      <CardHeader>
        <CardTitle className="text-base">Hoàn tiền</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 text-sm">
        {refunds.data.map((r) => (
          <RefundItem key={r.id} orderId={orderId} refund={r} />
        ))}
      </CardContent>
    </Card>
  );
}

function RefundItem({ orderId, refund }: { orderId: string; refund: BuyerRefund }) {
  const needsDestination =
    refund.stage === "awaiting_destination" || refund.stage === "destination_rejected";
  const [editing, setEditing] = useState(false);
  const showForm = refund.can_submit_destination && (needsDestination || editing);

  return (
    <div className="flex flex-col gap-2 rounded-md border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="font-medium">{formatMoney(refund.amount, refund.currency)}</span>
        <span className={refund.stage === "refunded" ? "text-success" : "text-muted-foreground"}>
          {BUYER_REFUND_STAGE_LABELS[refund.stage] ?? refund.stage}
        </span>
      </div>
      {refund.destination && refund.destination.status !== "superseded" && (
        <p className="text-muted-foreground">
          Tài khoản nhận: {refund.destination.masked}
          {refund.destination.status === "rejected" && refund.destination.decision_reason && (
            <span className="block text-destructive">
              Lý do: {refund.destination.decision_reason}
            </span>
          )}
        </p>
      )}
      <ol className="flex flex-col gap-1 text-xs text-muted-foreground">
        {refund.timeline.map((e) => (
          <li key={`${e.event}-${e.at}`}>
            {new Date(e.at).toLocaleString("vi-VN")} ·{" "}
            {BUYER_REFUND_EVENT_LABELS[e.event] ?? e.event}
          </li>
        ))}
      </ol>
      {refund.stage === "processing" && (
        <p className="text-xs text-muted-foreground">
          Tiền về tài khoản sau khi sàn xác nhận giao dịch với ngân hàng; thời gian phụ thuộc ngân
          hàng nhận.
        </p>
      )}
      {showForm ? (
        <DestinationForm
          orderId={orderId}
          refund={refund}
          onDone={() => setEditing(false)}
          onCancel={needsDestination ? undefined : () => setEditing(false)}
        />
      ) : (
        refund.can_submit_destination &&
        refund.stage === "verifying" && (
          <Button
            size="sm"
            variant="outline"
            className="self-start"
            onClick={() => setEditing(true)}
          >
            Đổi tài khoản nhận
          </Button>
        )
      )}
    </div>
  );
}

function DestinationForm({
  orderId,
  refund,
  onDone,
  onCancel,
}: {
  orderId: string;
  refund: BuyerRefund;
  onDone: () => void;
  onCancel?: () => void;
}) {
  const submit = useSubmitRefundDestination(orderId);
  const [form, setForm] = useState({ bank_code: "", account_number: "", account_name: "" });
  const [error, setError] = useState<string | null>(null);

  async function send(e: React.FormEvent) {
    e.preventDefault();
    const problem = checkBeneficiary(form);
    if (problem) {
      setError(problem);
      return;
    }
    setError(null);
    try {
      await submit.mutateAsync({
        refundId: refund.id,
        ...form,
        expected_version: refund.destination?.version ?? 0,
      });
      setForm({ bank_code: "", account_number: "", account_name: "" });
      onDone();
    } catch (err) {
      setError(refundDestinationError(err));
    }
  }

  return (
    <form onSubmit={send} className="flex flex-col gap-2" autoComplete="off">
      <p className="text-xs text-muted-foreground">
        Nhập tài khoản ngân hàng đứng tên bạn. Sàn chỉ chuyển tiền tới tài khoản đã xác minh; đừng
        gửi số tài khoản qua tin nhắn hỗ trợ.
      </p>
      <Label htmlFor={`bank-${refund.id}`}>Mã ngân hàng</Label>
      <Input
        id={`bank-${refund.id}`}
        maxLength={20}
        placeholder="VD: VCB"
        value={form.bank_code}
        onChange={(e) => setForm({ ...form, bank_code: e.target.value })}
      />
      <Label htmlFor={`account-${refund.id}`}>Số tài khoản</Label>
      <Input
        id={`account-${refund.id}`}
        inputMode="numeric"
        maxLength={40}
        value={form.account_number}
        onChange={(e) => setForm({ ...form, account_number: e.target.value })}
      />
      <Label htmlFor={`name-${refund.id}`}>Tên chủ tài khoản</Label>
      <Input
        id={`name-${refund.id}`}
        maxLength={120}
        value={form.account_name}
        onChange={(e) => setForm({ ...form, account_name: e.target.value })}
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={submit.isPending}>
          {submit.isPending ? "Đang gửi…" : "Gửi thông tin tài khoản"}
        </Button>
        {onCancel && (
          <Button type="button" size="sm" variant="ghost" onClick={onCancel}>
            Huỷ
          </Button>
        )}
      </div>
    </form>
  );
}
