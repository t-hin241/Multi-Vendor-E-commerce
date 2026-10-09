import { ApiError, type InboxItem } from "@/lib/api-client";

// AF-09 inbox helpers. The server decides what each person sees; these
// only format and keep navigation inside the app.

// POLL_MS: the bell's unread count refreshes this often while the tab is
// visible (TanStack Query pauses intervals in background tabs).
export const POLL_MS = 30_000;

// safeLink keeps an item's link inside the app: an absolute path, never a
// protocol-relative or absolute URL. Anything else opens the inbox.
export function safeLink(link: string): string {
  if (!link.startsWith("/") || link.startsWith("//") || link.includes("\\"))
    return "/notifications";
  return link;
}

export function unreadLabel(count: number): string {
  if (count <= 0) return "";
  return count > 99 ? "99+" : String(count);
}

// inboxDisabled: the feature flag is off (the bell and page hide).
export function inboxDisabled(error: unknown): boolean {
  return error instanceof ApiError && error.code === "feature_disabled";
}

export function inboxErrorMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 404) {
    return "Thông báo không còn tồn tại hoặc không thuộc tài khoản của bạn.";
  }
  return "Chưa tải được thông báo. Vui lòng thử lại.";
}

// timeAgo is a short relative time in Vietnamese ("5 phút trước").
export function timeAgo(iso: string, now: Date = new Date()): string {
  const seconds = Math.max(0, Math.floor((now.getTime() - new Date(iso).getTime()) / 1000));
  if (seconds < 60) return "Vừa xong";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} phút trước`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} giờ trước`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} ngày trước`;
  return new Date(iso).toLocaleDateString("vi-VN");
}

// newestId is the read marker for "đánh dấu tất cả đã đọc": the newest
// item the person has loaded, so items that arrive later stay unread.
export function newestId(pages: { items: InboxItem[] }[] | undefined): string | null {
  return pages?.[0]?.items[0]?.id ?? null;
}
