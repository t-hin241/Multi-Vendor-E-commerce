"use client";

import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { EyeOff } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { MarketingPreference } from "@/components/marketing-preference";
import { PageShell } from "@/components/page-shell";
import { EmptyState } from "@/components/states/empty-state";
import { ErrorState } from "@/components/states/error-state";
import { LoadingState } from "@/components/states/loading-state";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { inboxDisabled, inboxErrorMessage, newestId, safeLink, timeAgo } from "@/lib/inbox";
import { queryKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";

// The signed-in person's inbox (AF-09), for every role: newest first, 20
// per page. "Đánh dấu tất cả đã đọc" stops at the newest notice loaded, so
// one that arrives meanwhile stays unread. Hiding keeps the notice until it
// expires (90 days); the email is not affected.
export default function NotificationsPage() {
  const { user, isReady, callWithAuth } = useAuth();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (isReady && !user) router.replace("/login");
  }, [isReady, user, router]);

  const userId = user?.id ?? "";
  const inbox = useInfiniteQuery({
    queryKey: queryKeys.inboxPages(userId, unreadOnly),
    queryFn: ({ pageParam }) =>
      callWithAuth((token) =>
        api.listInbox(token, { cursor: pageParam || undefined, unread_only: unreadOnly }),
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: Boolean(user),
    retry: false,
  });

  if (!user) return null;
  if (inboxDisabled(inbox.error)) {
    return (
      <PageShell maxWidth="sm">
        <h1 className="text-2xl font-semibold tracking-tight">Thông báo</h1>
        <p className="mt-2 text-sm text-muted-foreground">Hộp thông báo chưa được bật.</p>
      </PageShell>
    );
  }

  const items = inbox.data?.pages.flatMap((p) => p.items) ?? [];
  const through = newestId(inbox.data?.pages);

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: queryKeys.inboxAll() });
  }

  async function act(fn: (token: string) => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await callWithAuth(fn);
    } catch (err) {
      setError(inboxErrorMessage(err));
    } finally {
      await refresh();
      setBusy(false);
    }
  }

  async function open(item: api.InboxItem) {
    if (!item.read_at) {
      try {
        await callWithAuth((token) => api.markInboxItemRead(token, item.id));
      } catch {
        // Opening still works; the item stays unread.
      }
      await refresh();
    }
    router.push(safeLink(item.link));
  }

  return (
    <PageShell maxWidth="sm">
      <h1 className="text-2xl font-semibold tracking-tight">Thông báo</h1>
      <p className="mt-1 text-sm text-muted-foreground">
        Những việc liên quan tới tài khoản của bạn. Thông báo được giữ 90 ngày; số tiền và trạng
        thái chính xác luôn xem ở trang chi tiết.
      </p>

      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant={unreadOnly ? "outline" : "default"}
          onClick={() => setUnreadOnly(false)}
        >
          Tất cả
        </Button>
        <Button
          size="sm"
          variant={unreadOnly ? "default" : "outline"}
          onClick={() => setUnreadOnly(true)}
        >
          Chưa đọc
        </Button>
        <Button
          size="sm"
          variant="ghost"
          className="ml-auto"
          disabled={!through || busy}
          onClick={() => through && act((token) => api.markInboxReadThrough(token, through))}
        >
          Đánh dấu tất cả đã đọc
        </Button>
      </div>
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}

      <div className="mt-4 flex flex-col gap-2">
        {inbox.isPending && <LoadingState rows={4} />}
        {inbox.isError && (
          <ErrorState message="Không tải được thông báo." onRetry={() => inbox.refetch()} />
        )}
        {inbox.data && items.length === 0 && (
          <EmptyState
            title={unreadOnly ? "Không có thông báo chưa đọc" : "Chưa có thông báo nào"}
          />
        )}
        {items.map((item) => (
          <Card key={item.id} className={cn(!item.read_at && "border-primary/40 bg-primary/5")}>
            <CardContent className="flex items-start gap-3 text-sm">
              <button
                type="button"
                className="min-w-0 flex-1 text-left"
                onClick={() => void open(item)}
              >
                <p className={cn("break-words", !item.read_at && "font-semibold")}>{item.title}</p>
                <p className="mt-1 break-words whitespace-pre-line text-muted-foreground">
                  {item.body}
                </p>
                <p className="mt-1 text-xs text-muted-foreground">{timeAgo(item.created_at)}</p>
              </button>
              <Button
                size="icon"
                variant="ghost"
                aria-label="Ẩn thông báo"
                disabled={busy}
                onClick={() => act((token) => api.hideInboxItem(token, item.id))}
              >
                <EyeOff className="size-4" />
              </Button>
            </CardContent>
          </Card>
        ))}
        {inbox.hasNextPage && (
          <Button
            variant="outline"
            disabled={inbox.isFetchingNextPage}
            onClick={() => inbox.fetchNextPage()}
          >
            {inbox.isFetchingNextPage ? "Đang tải…" : "Xem thêm"}
          </Button>
        )}
      </div>

      <MarketingPreference />
      <p className="mt-6 text-xs text-muted-foreground">
        Cần hỗ trợ về một đơn hàng? Mở{" "}
        <Link href="/orders" className="text-primary underline">
          trang đơn hàng
        </Link>
        .
      </p>
    </PageShell>
  );
}
