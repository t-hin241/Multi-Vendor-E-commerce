import { ApiError } from "@/lib/api-client";
import type {
  SupportCase,
  SupportCaseEvent,
  SupportCaseStatus,
  SupportCategory,
} from "@/lib/api-client";

// Presentation helpers for support cases. Order decides every status and
// permission; these only word them and hide buttons the server would
// refuse anyway.

export const SUPPORT_CATEGORIES: { value: SupportCategory; label: string; hint: string }[] = [
  {
    value: "not_received",
    label: "Chưa nhận được hàng",
    hint: "Đơn đã giao hoặc quá hạn nhưng bạn chưa nhận.",
  },
  {
    value: "missing_items",
    label: "Giao thiếu hàng",
    hint: "Kiện hàng thiếu sản phẩm hoặc số lượng.",
  },
  {
    value: "wrong_items",
    label: "Giao sai hàng",
    hint: "Sai mẫu, màu, kích cỡ hoặc sản phẩm khác.",
  },
  { value: "damaged", label: "Hàng bị hư hỏng", hint: "Hàng vỡ, móp, lỗi khi nhận." },
  {
    value: "payment_issue",
    label: "Thanh toán chưa rõ",
    hint: "Đã trả tiền nhưng đơn chưa cập nhật, hoặc bị trừ hai lần.",
  },
  { value: "other", label: "Vấn đề khác", hint: "Không ảnh hưởng tới tiền của đơn." },
];

const categoryLabels = Object.fromEntries(
  SUPPORT_CATEGORIES.map((c) => [c.value, c.label]),
) as Record<SupportCategory, string>;

export function supportCategoryLabel(category: string): string {
  return categoryLabels[category as SupportCategory] ?? category;
}

const statusLabels: Record<SupportCaseStatus, string> = {
  open: "Đã tiếp nhận",
  in_progress: "Đang xử lý",
  waiting_buyer: "Chờ người mua bổ sung",
  waiting_vendor: "Chờ người bán phản hồi",
  resolution_pending: "Chờ hoàn tiền / trả hàng",
  resolved: "Đã có kết luận",
  closed: "Đã đóng",
};

export function supportStatusLabel(status: string): string {
  return statusLabels[status as SupportCaseStatus] ?? status;
}

// Admin console wording (English, like the rest of the console).
const adminStatusLabels: Record<SupportCaseStatus, string> = {
  open: "Open (unassigned)",
  in_progress: "In progress",
  waiting_buyer: "Waiting for buyer",
  waiting_vendor: "Waiting for vendor",
  resolution_pending: "Waiting for refund/return",
  resolved: "Resolved",
  closed: "Closed",
};

export function adminSupportStatusLabel(status: string): string {
  return adminStatusLabels[status as SupportCaseStatus] ?? status;
}

export const SUPPORT_STATUS_OPTIONS = [
  "",
  "open",
  "in_progress",
  "waiting_buyer",
  "waiting_vendor",
  "resolution_pending",
  "resolved",
  "closed",
] as const;

const resolutionLabels: Record<string, string> = {
  no_action: "Giải thích, không cần hoàn tiền",
  refund: "Hoàn tiền",
  return: "Trả hàng hoàn tiền",
};

export function supportResolutionLabel(kind: string | null): string {
  return kind ? (resolutionLabels[kind] ?? kind) : "";
}

const actionLabels: Record<string, string> = {
  opened: "Mở yêu cầu",
  assigned: "Sàn tiếp nhận",
  status_changed: "Cập nhật trạng thái",
  buyer_replied: "Người mua phản hồi",
  vendor_replied: "Người bán phản hồi",
  resolution_proposed: "Sàn đề xuất hướng xử lý",
  resolution_confirmed: "Hoàn tiền / trả hàng đã xác nhận",
  resolution_failed: "Hoàn tiền / trả hàng chưa thành công",
  resolved: "Sàn kết luận",
  reopened: "Người mua mở lại",
  confirmed: "Người mua xác nhận",
  closed: "Sàn đóng yêu cầu",
  auto_closed: "Tự đóng sau thời hạn mở lại",
};

export function supportEventLabel(e: Pick<SupportCaseEvent, "action">): string {
  return actionLabels[e.action] ?? e.action.replace(/_/g, " ");
}

// isCaseActive: the case may still change, so its page polls for news.
export function isCaseActive(c: Pick<SupportCase, "status">): boolean {
  return c.status !== "closed";
}

// buyerCanReply: buyers add messages until the case is resolved; after
// that they confirm or reopen.
export function buyerCanReply(c: Pick<SupportCase, "status">): boolean {
  return c.status !== "resolved" && c.status !== "closed";
}

export function vendorCanReply(c: Pick<SupportCase, "status">): boolean {
  return c.status !== "resolved" && c.status !== "closed";
}

// reopenDeadline is when the buyer can no longer reopen a resolved case.
export function reopenDeadline(
  c: Pick<SupportCase, "status" | "resolved_at">,
  windowDays: number,
): Date | null {
  if (c.status !== "resolved" || !c.resolved_at) return null;
  return new Date(new Date(c.resolved_at).getTime() + windowDays * 24 * 60 * 60 * 1000);
}

export function canReopen(
  c: Pick<SupportCase, "status" | "resolved_at">,
  windowDays: number,
  now: Date = new Date(),
): boolean {
  const deadline = reopenDeadline(c, windowDays);
  return deadline !== null && now <= deadline;
}

export function isOverdue(
  c: Pick<SupportCase, "due_at" | "status">,
  now: Date = new Date(),
): boolean {
  return (
    Boolean(c.due_at) &&
    c.status !== "resolved" &&
    c.status !== "closed" &&
    new Date(c.due_at!) < now
  );
}

// adminCanResolve mirrors Order: a case is assigned first, and a pending
// resolution waits for its refund or return outcome.
export function adminCanResolve(c: Pick<SupportCase, "status">): boolean {
  return (
    c.status === "in_progress" || c.status === "waiting_buyer" || c.status === "waiting_vendor"
  );
}

// supportErrorMessage words the refusals a support form can get.
export function supportErrorMessage(err: unknown, fallback: string): string {
  if (!(err instanceof ApiError)) return fallback;
  switch (err.code) {
    case "case_already_open":
      return "Bạn đã có yêu cầu đang mở cho gói hàng và vấn đề này. Hãy trả lời trong yêu cầu đó.";
    case "version_conflict":
      return "Yêu cầu vừa được người khác cập nhật. Trang đã tải lại, vui lòng thử lại.";
    case "support_cases_disabled":
      return "Đơn hàng này chưa hỗ trợ gửi yêu cầu trực tuyến. Vui lòng liên hệ bộ phận hỗ trợ.";
    case "unsupported_attachment":
      return err.message || "Chỉ nhận ảnh JPEG hoặc PNG, tối đa 5 MiB.";
    case "attachments_unavailable":
      return "Tạm thời chưa tải ảnh lên được. Bạn có thể gửi nội dung trước và bổ sung ảnh sau.";
  }
  if (err.status === 0 || err.status >= 500) return `${fallback} ${err.message}`;
  return err.message || fallback;
}

// ACCEPTED_IMAGE_TYPES is checked before upload for a quick answer; the
// server sniffs the content and decides.
export const ACCEPTED_IMAGE_TYPES = ["image/jpeg", "image/png"];

export function checkImageFile(file: Pick<File, "type" | "size">, maxBytes: number): string | null {
  if (!ACCEPTED_IMAGE_TYPES.includes(file.type)) return "Chỉ nhận ảnh JPEG hoặc PNG.";
  if (file.size > maxBytes) return "Mỗi ảnh tối đa 5 MiB.";
  return null;
}
