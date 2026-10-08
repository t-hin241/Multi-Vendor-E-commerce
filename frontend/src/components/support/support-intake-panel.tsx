"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

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
import {
  INTAKE_REFERENCE_KINDS,
  checkIntakeReference,
  intakeStatusLabel,
  supportErrorMessage,
} from "@/lib/support-cases";

// SupportIntakePanel is for a buyer who paid but cannot find the order
// (PW-012): they send the reference they have, and the marketplace links
// it to their order. The buyer never chooses an order id here.
export function SupportIntakePanel({ enabled }: { enabled: boolean }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<api.SupportIntakeReferenceKind>("bank_transfer");
  const [reference, setReference] = useState("");
  const [message, setMessage] = useState("");
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [error, setError] = useState<string | null>(null);

  const intakes = useQuery({
    queryKey: ["my-support-intakes"],
    queryFn: () => callWithAuth((token) => api.listMySupportIntakes(token)),
    enabled,
  });
  const send = useMutation({
    mutationFn: () =>
      callWithAuth((token) =>
        api.createSupportIntake(
          token,
          { reference_kind: kind, reference: reference.trim(), message: message.trim() },
          key,
        ),
      ),
    onSuccess: async () => {
      setReference("");
      setMessage("");
      setKey(crypto.randomUUID());
      setOpen(false);
      await queryClient.invalidateQueries({ queryKey: ["my-support-intakes"] });
    },
    onError: (err) => setError(supportErrorMessage(err, "Không gửi được yêu cầu.")),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const problem = checkIntakeReference(reference);
    if (problem) {
      setError(problem);
      return;
    }
    if (!message.trim()) {
      setError("Vui lòng mô tả vấn đề.");
      return;
    }
    setError(null);
    send.mutate();
  }

  const items = intakes.data ?? [];
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle className="text-base">Không tìm thấy đơn hàng?</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <p className="text-muted-foreground">
          Nếu bạn đã thanh toán nhưng không thấy đơn, hãy gửi mã giao dịch hoặc mã thanh toán. Sàn
          sẽ tra cứu và gắn yêu cầu vào đơn của bạn.
        </p>
        {items.map((intake) => (
          <div key={intake.id} className="rounded-md border p-2">
            <p>
              <span className="font-mono">{intake.reference}</span> ·{" "}
              {intakeStatusLabel(intake.status)}
            </p>
            {intake.status === "linked" && intake.linked_case_id && (
              <Link className="text-primary underline" href={`/support/${intake.linked_case_id}`}>
                Xem yêu cầu hỗ trợ
              </Link>
            )}
            {intake.status === "closed" && intake.close_reason && (
              <p className="text-muted-foreground">{intake.close_reason}</p>
            )}
          </div>
        ))}
        {!open ? (
          <Button variant="outline" className="self-start" onClick={() => setOpen(true)}>
            Gửi yêu cầu không có mã đơn
          </Button>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-2">
            <Label>Loại mã</Label>
            <Select
              value={kind}
              onValueChange={(v) => setKind(v as api.SupportIntakeReferenceKind)}
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {INTAKE_REFERENCE_KINDS.map((k) => (
                  <SelectItem key={k.value} value={k.value}>
                    {k.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Label htmlFor="intake-reference">Mã</Label>
            <Input
              id="intake-reference"
              maxLength={100}
              value={reference}
              onChange={(e) => setReference(e.target.value)}
            />
            <Label htmlFor="intake-message">Mô tả</Label>
            <Textarea
              id="intake-message"
              rows={4}
              maxLength={4000}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
            />
            {error && <p className="text-destructive">{error}</p>}
            <div className="flex gap-2">
              <Button type="submit" disabled={send.isPending}>
                {send.isPending ? "Đang gửi…" : "Gửi yêu cầu"}
              </Button>
              <Button type="button" variant="ghost" onClick={() => setOpen(false)}>
                Huỷ
              </Button>
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
