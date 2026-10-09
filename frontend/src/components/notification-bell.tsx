"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { inboxDisabled, POLL_MS, safeLink, timeAgo, unreadLabel } from "@/lib/inbox";
import { queryKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";

// NotificationBell (AF-09) shows the signed-in person's unread count,
// polled every 30 s while the tab is visible, and the five newest notices.
// Opening one marks it read, then goes to its page (which checks access
// again). Hidden while the inbox is off. The wrapper always renders so the
// header layout does not move.
export function NotificationBell({ className }: { className?: string }) {
  const { user, callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const userId = user?.id ?? "";

  const unread = useQuery({
    queryKey: queryKeys.inboxUnread(userId),
    queryFn: () => callWithAuth((token) => api.getInboxUnreadCount(token)),
    enabled: Boolean(user),
    refetchInterval: POLL_MS,
    retry: false,
  });
  const preview = useQuery({
    queryKey: queryKeys.inboxPreview(userId),
    queryFn: () => callWithAuth((token) => api.listInbox(token, { limit: 5 })),
    enabled: Boolean(user) && open,
  });

  if (!user || unread.isPending || inboxDisabled(unread.error)) {
    return <div className={className} />;
  }
  const count = unread.data?.unread_count ?? 0;

  async function openItem(item: api.InboxItem) {
    setOpen(false);
    if (!item.read_at) {
      try {
        await callWithAuth((token) => api.markInboxItemRead(token, item.id));
      } catch {
        // Opening still works; the item stays unread until the next try.
      }
      await queryClient.invalidateQueries({ queryKey: queryKeys.inboxAll() });
    }
    router.push(safeLink(item.link));
  }

  return (
    <div className={className}>
      <DropdownMenu open={open} onOpenChange={setOpen}>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className="relative"
            aria-label={count > 0 ? `Thông báo, ${count} chưa đọc` : "Thông báo"}
          >
            <Bell className="size-5" />
            {count > 0 && (
              <Badge className="absolute top-0 right-0 h-4 min-w-4 justify-center rounded-full px-1 text-[10px]">
                {unreadLabel(count)}
              </Badge>
            )}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-80 max-w-[calc(100vw-2rem)]">
          <DropdownMenuLabel>Thông báo</DropdownMenuLabel>
          <DropdownMenuSeparator />
          {preview.isPending && (
            <p className="px-2 py-3 text-sm text-muted-foreground">Đang tải…</p>
          )}
          {preview.isError && (
            <p className="px-2 py-3 text-sm text-destructive">Chưa tải được thông báo.</p>
          )}
          {preview.data?.items.length === 0 && (
            <p className="px-2 py-3 text-sm text-muted-foreground">Chưa có thông báo nào.</p>
          )}
          {preview.data?.items.map((item) => (
            <DropdownMenuItem
              key={item.id}
              className="flex flex-col items-start gap-0.5"
              onSelect={(e) => {
                e.preventDefault();
                void openItem(item);
              }}
            >
              <span className={cn("line-clamp-2 text-sm", !item.read_at && "font-semibold")}>
                {item.title}
              </span>
              <span className="text-xs text-muted-foreground">{timeAgo(item.created_at)}</span>
            </DropdownMenuItem>
          ))}
          <DropdownMenuSeparator />
          <DropdownMenuItem asChild>
            <Link href="/notifications" className="justify-center text-sm text-primary">
              Xem tất cả
            </Link>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}
