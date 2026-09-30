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
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { OrderItem } from "@/lib/api-client";
import { formatMoney } from "@/lib/format";

// ReturnRequestDialog asks for the quantity, reason and evidence of a
// return. The refund shown is the item price times quantity; shipping is
// not refunded. Order checks the return window and quantity again.
export function ReturnRequestDialog({
  item,
  currency,
  maxQuantity,
  pending,
  onSubmit,
}: {
  item: OrderItem;
  currency: string;
  maxQuantity: number;
  pending: boolean;
  onSubmit: (input: { quantity: number; reason: string; evidence?: string }) => Promise<unknown>;
}) {
  const [open, setOpen] = useState(false);
  const [quantity, setQuantity] = useState(1);
  const [reason, setReason] = useState("");
  const [evidence, setEvidence] = useState("");
  const valid = quantity >= 1 && quantity <= maxQuantity && reason.trim().length > 0;

  async function submit() {
    try {
      await onSubmit({ quantity, reason: reason.trim(), evidence: evidence.trim() || undefined });
      setOpen(false);
      setReason("");
      setEvidence("");
      setQuantity(1);
    } catch {
      // The mutation already shows the error; keep the form open.
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger asChild>
        <Button variant="outline" size="sm" disabled={maxQuantity < 1 || pending}>
          Trả hàng: {item.product_name}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Yêu cầu trả {item.product_name}</AlertDialogTitle>
          <AlertDialogDescription>
            Người bán xác nhận trước, sau đó sàn duyệt. Tiền được hoàn sau khi hàng về đến người
            bán. Phí vận chuyển không được hoàn.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1">
            <Label htmlFor="return-quantity">Số lượng (tối đa {maxQuantity})</Label>
            <Input
              id="return-quantity"
              type="number"
              min={1}
              max={maxQuantity}
              value={quantity}
              onChange={(e) => setQuantity(Math.floor(Number(e.target.value)) || 0)}
            />
            <p className="text-xs text-muted-foreground">
              Dự kiến hoàn {formatMoney(item.price_amount * Math.max(0, quantity), currency)}
            </p>
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="return-reason">Lý do</Label>
            <Textarea
              id="return-reason"
              rows={3}
              maxLength={2000}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="return-evidence">Bằng chứng (mô tả hoặc đường dẫn ảnh)</Label>
            <Textarea
              id="return-evidence"
              rows={2}
              maxLength={2000}
              value={evidence}
              onChange={(e) => setEvidence(e.target.value)}
            />
          </div>
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Đóng</AlertDialogCancel>
          <Button disabled={!valid || pending} onClick={submit}>
            {pending ? "Đang gửi…" : "Gửi yêu cầu"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
