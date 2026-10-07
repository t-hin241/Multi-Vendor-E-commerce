"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

import { AttachmentPicker, type PickedAttachment } from "@/components/support/attachment-picker";
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
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import type { Order, SupportCapability, SupportCategory } from "@/lib/api-client";
import { formatMoney } from "@/lib/format";
import { useCreateSupportCase } from "@/lib/hooks/use-support-cases";
import { newIdempotencyKey } from "@/lib/order-workflow";
import { SUPPORT_CATEGORIES, supportErrorMessage } from "@/lib/support-cases";

// OpenSupportCaseDialog lets a buyer report a problem with one package of
// their order. Order checks ownership, the order state and that no case is
// already open for the same package and topic; the buyer gets a reference
// to follow the case.
export function OpenSupportCaseDialog({
  order,
  capability,
}: {
  order: Order;
  capability: SupportCapability;
}) {
  const router = useRouter();
  const create = useCreateSupportCase(order.id);
  const packages = order.vendor_orders ?? [];
  const [open, setOpen] = useState(false);
  const [vendorOrderId, setVendorOrderId] = useState(packages[0]?.id ?? "");
  const [category, setCategory] = useState<SupportCategory | "">("");
  const [message, setMessage] = useState("");
  const [attachments, setAttachments] = useState<PickedAttachment[]>([]);
  // One key per form: a resend after a lost response returns the same case.
  const [key, setKey] = useState(newIdempotencyKey);
  const [error, setError] = useState<string | null>(null);

  const valid = Boolean(vendorOrderId && category && message.trim());

  function reset() {
    setCategory("");
    setMessage("");
    setAttachments([]);
    setError(null);
    setKey(newIdempotencyKey());
  }

  async function submit() {
    if (!category) return;
    setError(null);
    try {
      const created = await create.mutateAsync({
        input: {
          vendorOrderId,
          category,
          message: message.trim(),
          attachmentIds: attachments.map((a) => a.id),
        },
        idempotencyKey: key,
      });
      setOpen(false);
      reset();
      router.push(`/support/${created.id}`);
    } catch (err) {
      setError(supportErrorMessage(err, "Không gửi được yêu cầu hỗ trợ."));
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <AlertDialogTrigger asChild>
        <Button variant="outline" size="sm" disabled={packages.length === 0}>
          Cần hỗ trợ
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Yêu cầu hỗ trợ cho đơn #{order.id.slice(0, 8)}</AlertDialogTitle>
          <AlertDialogDescription>
            Người bán và sàn sẽ phản hồi trong yêu cầu này. Mọi khoản hoàn tiền chỉ được xác nhận
            khi cổng thanh toán báo đã hoàn.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <div className="flex flex-col gap-3">
          {packages.length > 1 && (
            <div className="flex flex-col gap-1">
              <Label>Gói hàng</Label>
              <Select value={vendorOrderId} onValueChange={setVendorOrderId}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {packages.map((vo, idx) => (
                    <SelectItem key={vo.id} value={vo.id}>
                      Gói hàng {idx + 1} · {formatMoney(vo.subtotal_amount, vo.currency)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <div className="flex flex-col gap-1">
            <Label>Vấn đề</Label>
            <Select value={category} onValueChange={(v) => setCategory(v as SupportCategory)}>
              <SelectTrigger>
                <SelectValue placeholder="Chọn vấn đề" />
              </SelectTrigger>
              <SelectContent>
                {SUPPORT_CATEGORIES.map((c) => (
                  <SelectItem key={c.value} value={c.value}>
                    {c.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {category && (
              <p className="text-xs text-muted-foreground">
                {SUPPORT_CATEGORIES.find((c) => c.value === category)?.hint}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="support-message">Mô tả</Label>
            <Textarea
              id="support-message"
              rows={4}
              maxLength={capability.max_message_chars}
              value={message}
              placeholder="Mô tả điều đã xảy ra. Không ghi mật khẩu hoặc số thẻ."
              onChange={(e) => setMessage(e.target.value)}
            />
          </div>
          {capability.attachments_enabled && (
            <div className="flex flex-col gap-1">
              <Label>Ảnh bằng chứng (JPEG/PNG, tối đa {capability.max_attachments} ảnh)</Label>
              <AttachmentPicker
                scope="buyer"
                value={attachments}
                onChange={setAttachments}
                max={capability.max_attachments}
                maxBytes={capability.max_attachment_bytes}
                disabled={create.isPending}
              />
            </div>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>
        <AlertDialogFooter>
          <AlertDialogCancel>Đóng</AlertDialogCancel>
          <Button disabled={!valid || create.isPending} onClick={submit}>
            {create.isPending ? "Đang gửi…" : "Gửi yêu cầu"}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
