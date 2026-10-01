"use client";

import { useQuery } from "@tanstack/react-query";
import { CheckCircle2, Clock, Loader2, XCircle } from "lucide-react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";

import { PageShell } from "@/components/page-shell";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { formatMoney } from "@/lib/format";
import { useCancelOrder } from "@/lib/hooks/use-orders";
import { orderIdFromQuery, paymentView, pollDelay } from "@/lib/payment-polling";
import { queryKeys } from "@/lib/query-keys";

// PaymentOutcome is where the payment provider sends the buyer back
// (returned: the return URL; otherwise the cancel URL). It shows only what
// Order reports, looking again with backoff for a while: the provider's
// query string cannot mark an order paid or cancelled here.
export function PaymentOutcome({ returned }: { returned: boolean }) {
  const params = useSearchParams();
  const orderId = orderIdFromQuery(params.get("order_id"));
  const { user, isReady, callWithAuth } = useAuth();
  const [round, setRound] = useState(() => ({ startedAt: Date.now(), timedOut: false }));
  const maxMs = returned ? 120_000 : 30_000;
  const enabled = Boolean(orderId && user?.role === "buyer");

  useEffect(() => {
    if (!enabled) return;
    const timer = setTimeout(
      () => setRound((r) => ({ ...r, timedOut: true })),
      Math.max(0, round.startedAt + maxMs - Date.now()),
    );
    return () => clearTimeout(timer);
  }, [enabled, round.startedAt, maxMs]);

  const order = useQuery({
    queryKey: queryKeys.order(orderId ?? "none"),
    queryFn: () => callWithAuth((token) => api.getOrder(token, orderId!)),
    enabled,
    retry: (count, err) => count < 3 && api.isOutcomeUnknown(err),
    refetchInterval: (query) => {
      const view = paymentView(query.state.data, returned, false);
      if (view === "paid" || view === "cancelled") return false;
      const looks = query.state.dataUpdateCount + query.state.errorUpdateCount;
      return pollDelay(looks, Date.now() - round.startedAt, maxMs) ?? false;
    },
  });
  const cancel = useCancelOrder(orderId ?? "");

  if (!isReady) return null;
  if (!orderId) {
    return (
      <Outcome
        icon={<XCircle className="size-10 text-muted-foreground" />}
        title="Không xác định được đơn hàng"
        text="Đường dẫn quay về từ cổng thanh toán không hợp lệ. Bạn có thể xem trạng thái trong Đơn hàng của tôi."
        actions={
          <Button asChild>
            <Link href="/orders">Đơn hàng của tôi</Link>
          </Button>
        }
      />
    );
  }
  if (!user || user.role !== "buyer") {
    return (
      <Outcome
        icon={<Clock className="size-10 text-muted-foreground" />}
        title="Vui lòng đăng nhập"
        text="Đăng nhập bằng tài khoản đã đặt đơn để xem kết quả thanh toán."
        actions={
          <Button asChild>
            <Link href="/login">Đăng nhập</Link>
          </Button>
        }
      />
    );
  }
  if (order.error && !order.data && !api.isOutcomeUnknown(order.error)) {
    return (
      <Outcome
        icon={<XCircle className="size-10 text-destructive" />}
        title="Không xem được đơn hàng này"
        text={describeApiError(order.error, "Không thể tải đơn hàng.")}
        actions={
          <Button asChild>
            <Link href="/orders">Đơn hàng của tôi</Link>
          </Button>
        }
      />
    );
  }

  const view = paymentView(order.data, returned, round.timedOut);
  const total = order.data ? formatMoney(order.data.total_amount, order.data.currency) : null;
  const toOrder = (
    <Button asChild variant={view === "paid" ? "default" : "outline"}>
      <Link href={`/orders/${orderId}`}>Xem đơn hàng</Link>
    </Button>
  );

  switch (view) {
    case "paid":
      return (
        <Outcome
          icon={<CheckCircle2 className="size-10 text-success" />}
          title="Thanh toán thành công"
          text={`Đơn hàng${total ? ` ${total}` : ""} đã được xác nhận thanh toán. Người bán sẽ chuẩn bị hàng.`}
          actions={toOrder}
        />
      );
    case "cancelled":
      return (
        <Outcome
          icon={<XCircle className="size-10 text-muted-foreground" />}
          title="Đơn hàng đã hủy"
          text="Đơn hàng này đã bị hủy và không cần thanh toán."
          actions={toOrder}
        />
      );
    case "unpaid":
      return (
        <Outcome
          icon={<Clock className="size-10 text-warning" />}
          title="Bạn chưa hoàn tất thanh toán"
          text="Đơn hàng vẫn đang chờ thanh toán. Bạn có thể thanh toán lại, hoặc hủy đơn nếu không muốn mua nữa."
          actions={
            <>
              <Button asChild>
                <Link href={`/orders/${orderId}`}>Thanh toán lại</Link>
              </Button>
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button
                    variant="outline"
                    className="text-destructive"
                    disabled={cancel.isPending}
                  >
                    {cancel.isPending ? "Đang hủy…" : "Hủy đơn hàng"}
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>Hủy đơn hàng này?</AlertDialogTitle>
                    <AlertDialogDescription>
                      Không thể hoàn tác. Nếu bạn vừa thanh toán xong, hãy đợi vài phút để đơn cập
                      nhật trước khi hủy.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>Giữ đơn hàng</AlertDialogCancel>
                    <AlertDialogAction onClick={() => cancel.mutate()}>
                      Hủy đơn hàng
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </>
          }
        />
      );
    case "unknown":
      return (
        <Outcome
          icon={<Clock className="size-10 text-warning" />}
          title="Chưa nhận được xác nhận thanh toán"
          text="Nếu bạn đã thanh toán, đơn hàng sẽ tự cập nhật khi cổng thanh toán báo về; đừng thanh toán lại để tránh bị trừ tiền hai lần. Bạn có thể kiểm tra lại sau ít phút."
          actions={
            <>
              <Button
                onClick={() => {
                  setRound({ startedAt: Date.now(), timedOut: false });
                  void order.refetch();
                }}
              >
                Kiểm tra lại
              </Button>
              {toOrder}
            </>
          }
        />
      );
    default:
      return (
        <Outcome
          icon={<Loader2 className="size-10 animate-spin text-muted-foreground" />}
          title="Đang xác nhận thanh toán…"
          text="Chúng tôi đang chờ cổng thanh toán xác nhận. Việc này thường mất vài giây."
          actions={toOrder}
        />
      );
  }
}

function Outcome({
  icon,
  title,
  text,
  actions,
}: {
  icon: React.ReactNode;
  title: string;
  text: string;
  actions: React.ReactNode;
}) {
  return (
    <PageShell maxWidth="sm">
      <Card>
        <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
          {icon}
          <h1 className="text-xl font-semibold" role="status" aria-live="polite">
            {title}
          </h1>
          <p className="max-w-md text-sm text-muted-foreground">{text}</p>
          <div className="mt-2 flex flex-wrap justify-center gap-2">{actions}</div>
        </CardContent>
      </Card>
    </PageShell>
  );
}
