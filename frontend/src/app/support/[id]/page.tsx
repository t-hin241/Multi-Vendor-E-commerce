"use client";

import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { PageShell } from "@/components/page-shell";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import { SupportStatusBadge } from "@/components/support/support-status-badge";
import { SupportThread } from "@/components/support/support-thread";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  useSupportCapability,
  useSupportCase,
  useSupportCaseRefresh,
} from "@/lib/hooks/use-support-cases";
import {
  buyerCanReply,
  canReopen,
  reopenDeadline,
  supportCategoryLabel,
  supportErrorMessage,
  supportResolutionLabel,
} from "@/lib/support-cases";

// A buyer's support case: the conversation with the shop and the
// marketplace, the conclusion, and confirming or reopening it.
export default function SupportCasePage() {
  const params = useParams<{ id: string }>();
  const { user, isReady, callWithAuth } = useAuth();
  const router = useRouter();
  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) router.replace("/login");
  }, [isReady, user, router]);
  const enabled = Boolean(user && user.role === "buyer");
  const caseQuery = useSupportCase("buyer", params.id, enabled);
  const capability = useSupportCapability(enabled);
  const refresh = useSupportCaseRefresh("buyer", params.id);
  const [reopening, setReopening] = useState(false);
  const [reopenMessage, setReopenMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!enabled) return null;
  if (caseQuery.isPending) {
    return (
      <PageShell maxWidth="sm">
        <LoadingState rows={4} />
      </PageShell>
    );
  }
  if (caseQuery.error || !caseQuery.data) {
    return (
      <PageShell maxWidth="sm">
        {caseQuery.error instanceof api.ApiError && caseQuery.error.status === 404 ? (
          <EmptyState title="Không tìm thấy yêu cầu hỗ trợ" />
        ) : (
          <ErrorState message="Không tải được yêu cầu hỗ trợ." onRetry={caseQuery.refetch} />
        )}
      </PageShell>
    );
  }

  const c = caseQuery.data;
  const windowDays = capability.data?.reopen_window_days ?? 7;
  const deadline = reopenDeadline(c, windowDays);

  async function act(fn: (token: string) => Promise<unknown>, fallback: string) {
    setBusy(true);
    setError(null);
    try {
      await callWithAuth(fn);
      setReopening(false);
      setReopenMessage("");
      await refresh();
    } catch (err) {
      setError(supportErrorMessage(err, fallback));
    } finally {
      setBusy(false);
    }
  }

  return (
    <PageShell maxWidth="sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-2xl font-semibold tracking-tight">
          {supportCategoryLabel(c.category)}
        </h1>
        <SupportStatusBadge status={c.status} />
      </div>
      <p className="mt-1 text-sm text-muted-foreground">
        Mã tra cứu #{c.id.slice(0, 8)} ·{" "}
        <Link href={`/orders/${c.order_id}`} className="text-primary underline">
          đơn #{c.order_id.slice(0, 8)}
        </Link>{" "}
        · mở lúc {new Date(c.created_at).toLocaleString("vi-VN")}
      </p>
      {c.status === "resolution_pending" && (
        <p className="mt-2 text-sm">
          Sàn đã đề xuất {supportResolutionLabel(c.resolution_kind).toLowerCase()}; yêu cầu có kết
          luận khi khoản hoàn tiền hoặc trả hàng được xác nhận.
        </p>
      )}

      {(c.status === "resolved" || c.status === "closed") && c.resolution_kind && (
        <Card className="mt-4">
          <CardHeader>
            <CardTitle className="text-base">Kết luận</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-2 text-sm">
            <p className="font-medium">{supportResolutionLabel(c.resolution_kind)}</p>
            {c.resolution_note && <p className="whitespace-pre-wrap">{c.resolution_note}</p>}
            {c.status === "resolved" && (
              <>
                <div className="flex flex-wrap gap-2">
                  <Button
                    disabled={busy}
                    onClick={() =>
                      act((t) => api.confirmSupportCase(t, c.id), "Không xác nhận được.")
                    }
                  >
                    Đồng ý, đóng yêu cầu
                  </Button>
                  {canReopen(c, windowDays) && !reopening && (
                    <Button variant="outline" disabled={busy} onClick={() => setReopening(true)}>
                      Chưa đồng ý, mở lại
                    </Button>
                  )}
                </div>
                {deadline && (
                  <p className="text-xs text-muted-foreground">
                    Có thể mở lại đến {deadline.toLocaleString("vi-VN")}; sau đó yêu cầu tự đóng.
                  </p>
                )}
                {reopening && (
                  <div className="flex flex-col gap-2">
                    <Textarea
                      rows={3}
                      maxLength={4000}
                      value={reopenMessage}
                      placeholder="Vì sao bạn chưa đồng ý?"
                      onChange={(e) => setReopenMessage(e.target.value)}
                    />
                    <div className="flex gap-2">
                      <Button
                        disabled={busy || !reopenMessage.trim()}
                        onClick={() =>
                          act(
                            (t) => api.reopenSupportCase(t, c.id, reopenMessage.trim()),
                            "Không mở lại được.",
                          )
                        }
                      >
                        Mở lại
                      </Button>
                      <Button variant="ghost" onClick={() => setReopening(false)}>
                        Thôi
                      </Button>
                    </div>
                  </div>
                )}
              </>
            )}
            {error && <p className="text-destructive">{error}</p>}
          </CardContent>
        </Card>
      )}
      {c.status === "closed" && (
        <p className="mt-4 text-sm text-muted-foreground">
          Yêu cầu đã đóng. Nếu vẫn còn vấn đề, hãy mở yêu cầu mới từ trang đơn hàng.
        </p>
      )}

      <div className="mt-4">
        <SupportThread
          scope="buyer"
          detail={c}
          canReply={buyerCanReply(c)}
          capability={capability.data}
          onChanged={refresh}
        />
      </div>
    </PageShell>
  );
}
