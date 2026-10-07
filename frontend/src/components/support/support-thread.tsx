"use client";

import { useState } from "react";

import { ActionDeadline } from "@/components/support/action-deadline";

import { AttachmentImage } from "@/components/support/attachment-image";
import { AttachmentPicker, type PickedAttachment } from "@/components/support/attachment-picker";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { newIdempotencyKey } from "@/lib/order-workflow";
import { supportErrorMessage, supportEventLabel } from "@/lib/support-cases";
import { cn } from "@/lib/utils";

const TEXT = {
  vi: {
    messages: "Trao đổi",
    timeline: "Lịch sử xử lý",
    none: "Chưa có tin nhắn.",
    reply: "Trả lời",
    send: "Gửi",
    sending: "Đang gửi…",
    placeholder: "Nhập nội dung (văn bản thuần, tối đa 4.000 ký tự)",
    internal: "Ghi chú nội bộ",
    failed: "Không gửi được tin nhắn.",
    authors: { buyer: "Người mua", vendor: "Người bán", admin: "Sàn", system: "Hệ thống" },
    you: "Bạn",
  },
  en: {
    messages: "Messages",
    timeline: "Timeline",
    none: "No messages yet.",
    reply: "Reply",
    send: "Send",
    sending: "Sending…",
    placeholder: "Plain text, up to 4,000 characters",
    internal: "Internal note (admins only)",
    failed: "The message was not sent.",
    authors: { buyer: "Buyer", vendor: "Vendor", admin: "Admin", system: "System" },
    you: "You",
  },
} as const;

function formatTime(iso: string) {
  return new Date(iso).toLocaleString("vi-VN", { timeZone: "Asia/Saigon" });
}

// SupportThread shows a case's messages and timeline as the viewer may see
// them (Order already removed what the role must not see) and the reply
// form. One Idempotency-Key per draft: a resend after a lost response
// returns the same message instead of posting it twice.
export function SupportThread({
  scope,
  detail,
  canReply,
  locale = "vi",
  capability,
  onChanged,
}: {
  scope: api.SupportScope;
  detail: api.SupportCaseDetail;
  canReply: boolean;
  locale?: "vi" | "en";
  capability?: Pick<
    api.SupportCapability,
    "attachments_enabled" | "max_attachments" | "max_attachment_bytes"
  >;
  onChanged: () => Promise<void>;
}) {
  const t = TEXT[locale];
  const { callWithAuth, user } = useAuth();
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<PickedAttachment[]>([]);
  const [internal, setInternal] = useState(false);
  const [draftKey, setDraftKey] = useState(newIdempotencyKey);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function send() {
    setSending(true);
    setError(null);
    try {
      await callWithAuth((token) =>
        api.postSupportMessage(
          token,
          scope,
          detail.id,
          {
            text: text.trim(),
            attachmentIds: attachments.map((a) => a.id),
            visibility: scope === "admin" && internal ? "internal" : undefined,
          },
          draftKey,
        ),
      );
      setText("");
      setAttachments([]);
      setInternal(false);
      setDraftKey(newIdempotencyKey());
      await onChanged();
    } catch (err) {
      setError(supportErrorMessage(err, t.failed));
    } finally {
      setSending(false);
    }
  }

  const attachmentsEnabled = capability?.attachments_enabled ?? true;

  return (
    <div className="flex flex-col gap-4">
      <ActionDeadline dueAt={detail.action_due_at} waitingOn={detail.waiting_on} />
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t.messages}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {detail.messages.length === 0 && (
            <p className="text-sm text-muted-foreground">{t.none}</p>
          )}
          {detail.messages.map((m) => {
            const mine = Boolean(user && m.author_id === user.id);
            return (
              <div
                key={m.id}
                className={cn(
                  "rounded-md border p-3 text-sm",
                  m.visibility === "internal" && "border-amber-500/50 bg-amber-500/10",
                  mine && "bg-muted/50",
                )}
              >
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                  <span className="font-medium text-foreground">
                    {mine ? t.you : t.authors[m.author_role]}
                    {m.visibility === "internal" && ` · ${t.internal}`}
                  </span>
                  <time dateTime={m.created_at}>{formatTime(m.created_at)}</time>
                </div>
                {/* Plain text only: React escapes it; Order refuses HTML. */}
                <p className="mt-1 whitespace-pre-wrap break-words">{m.text}</p>
                {m.attachments.length > 0 && (
                  <div className="mt-2 flex flex-wrap gap-2">
                    {m.attachments.map((a) => (
                      <AttachmentImage key={a.id} scope={scope} caseId={detail.id} attachment={a} />
                    ))}
                  </div>
                )}
              </div>
            );
          })}

          {canReply && (
            <div className="flex flex-col gap-2 border-t pt-3">
              <Label htmlFor="support-reply">{t.reply}</Label>
              <Textarea
                id="support-reply"
                rows={3}
                maxLength={4000}
                value={text}
                placeholder={t.placeholder}
                onChange={(e) => setText(e.target.value)}
              />
              {attachmentsEnabled && capability && (
                <AttachmentPicker
                  scope={scope}
                  value={attachments}
                  onChange={setAttachments}
                  max={capability.max_attachments}
                  maxBytes={capability.max_attachment_bytes}
                  disabled={sending}
                  label={locale === "en" ? "Add image" : "Thêm ảnh"}
                />
              )}
              <div className="flex flex-wrap items-center justify-between gap-2">
                {scope === "admin" ? (
                  <label className="flex items-center gap-2 text-sm">
                    <Switch checked={internal} onCheckedChange={setInternal} />
                    {t.internal}
                  </label>
                ) : (
                  <span />
                )}
                <Button disabled={!text.trim() || sending} onClick={send}>
                  {sending ? t.sending : t.send}
                </Button>
              </div>
              {error && <p className="text-sm text-destructive">{error}</p>}
            </div>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t.timeline}</CardTitle>
        </CardHeader>
        <CardContent>
          <ol className="flex flex-col gap-2 text-sm">
            {detail.events.map((e) => (
              <li key={e.id} className="flex flex-col">
                <span>
                  {supportEventLabel(e)}
                  {scope === "admin" && (
                    <span className="text-muted-foreground">
                      {" "}
                      ({e.from_status ?? "—"} → {e.to_status}, {t.authors[e.actor_role]}
                      {e.actor_id ? ` ${e.actor_id.slice(0, 8)}` : ""})
                    </span>
                  )}
                </span>
                {e.note && <span className="text-xs text-muted-foreground">{e.note}</span>}
                <time className="text-xs text-muted-foreground" dateTime={e.created_at}>
                  {formatTime(e.created_at)}
                </time>
              </li>
            ))}
          </ol>
        </CardContent>
      </Card>
    </div>
  );
}
