"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ApiError } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import {
  availabilityErrorMessage,
  formatVietnamTime,
  getShopAvailability,
  instantToVietnamInput,
  resumeShop,
  scheduleVacation,
  type ShopAvailability,
  vietnamInputToInstant,
} from "@/lib/shop-availability";

// VacationSchedule: the owner (or staff with shop.availability.write)
// pauses new orders for a period (AF-18). Existing orders, returns and
// payouts go on; the page says so, and says when the pause has not reached
// checkout yet (enforcement_pending).
export function VacationSchedule({ vendorId, canWrite }: { vendorId: string; canWrite: boolean }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const key = ["shop-availability", vendorId];
  const availability = useQuery({
    queryKey: key,
    queryFn: () => callWithAuth((token) => getShopAvailability(token, vendorId)),
    // While the pause is reaching checkout, look again shortly.
    refetchInterval: (q) => (q.state.data?.enforcement_pending ? 5000 : false),
  });
  const [form, setForm] = useState<{ start: string; end: string; message: string } | null>(null);
  const [error, setError] = useState<string | null>(null);

  const current = availability.data?.current;
  const values = form ?? {
    start: instantToVietnamInput(current?.starts_at),
    end: instantToVietnamInput(current?.ends_at),
    message: current?.message ?? "",
  };

  function onFailure(err: unknown) {
    if (err instanceof ApiError) {
      if (err.code === "version_conflict") {
        setForm(null);
        void queryClient.invalidateQueries({ queryKey: key });
      }
      setError(availabilityErrorMessage(err.code, describeApiError(err)));
      return;
    }
    setError(describeApiError(err, "Không lưu được lịch tạm nghỉ."));
  }
  function onSaved(next: ShopAvailability, message: string) {
    queryClient.setQueryData(key, next);
    setForm(null);
    setError(null);
    toast.success(message);
  }

  const save = useMutation({
    mutationFn: (version: number) => {
      const start = vietnamInputToInstant(values.start);
      const end = vietnamInputToInstant(values.end);
      if (start === null || end === null) throw new Error("Thời gian không hợp lệ.");
      return callWithAuth((token) =>
        scheduleVacation(token, vendorId, {
          // An active vacation keeps its start.
          starts_at: current?.status === "active" ? current.starts_at : start,
          ends_at: end,
          message: values.message,
          expected_version: version,
        }),
      );
    },
    onSuccess: (next) => onSaved(next, "Đã lưu lịch tạm nghỉ."),
    onError: onFailure,
  });
  const resume = useMutation({
    mutationFn: (version: number) => callWithAuth((token) => resumeShop(token, vendorId, version)),
    onSuccess: (next) => onSaved(next, "Đã cập nhật trạng thái bán."),
    onError: onFailure,
  });

  if (availability.isPending) return <p className="text-sm text-muted-foreground">Đang tải…</p>;
  if (availability.isError || !availability.data) {
    return (
      <p role="alert" className="text-sm text-destructive">
        {describeApiError(availability.error, "Không tải được trạng thái bán.")}
      </p>
    );
  }
  const a = availability.data;
  const busy = save.isPending || resume.isPending;
  const editable = canWrite && (a.enabled || !!current);

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            Trạng thái nhận đơn
            {a.accepting_orders ? (
              <Badge variant="secondary">Đang nhận đơn</Badge>
            ) : (
              <Badge variant="destructive">Đang tạm nghỉ</Badge>
            )}
            {a.enforcement_pending && <Badge variant="outline">Đang đồng bộ</Badge>}
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 text-sm">
          {a.shop_status === "suspended" && (
            <p className="text-destructive">
              Cửa hàng đang bị sàn tạm khóa. Kết thúc tạm nghỉ không mở lại cửa hàng bị khóa.
            </p>
          )}
          {current?.status === "active" && (
            <p>
              Tạm nghỉ từ {formatVietnamTime(current.paused_at ?? current.starts_at)}
              {current.ends_at
                ? `, tự mở bán lại lúc ${formatVietnamTime(current.ends_at)}.`
                : ", chưa hẹn ngày mở bán lại."}
            </p>
          )}
          {current?.status === "scheduled" && (
            <p>
              Đã hẹn tạm nghỉ từ {formatVietnamTime(current.starts_at)}
              {current.ends_at ? ` đến ${formatVietnamTime(current.ends_at)}` : ""}.
            </p>
          )}
          {!current && <p className="text-muted-foreground">Không có lịch tạm nghỉ.</p>}
          {a.enforcement_pending && (
            <p className="text-muted-foreground">
              Thay đổi đang được đồng bộ tới giỏ hàng và thanh toán; trong lúc này một số đơn vẫn có
              thể được đặt theo trạng thái trước đó.
            </p>
          )}
          <p className="text-muted-foreground">
            Khi tạm nghỉ, người mua không đặt được đơn mới. Đơn đã đặt, trả hàng, hoàn tiền và thanh
            toán cho shop vẫn tiếp tục; hãy xử lý đơn cũ đúng hạn.
          </p>
          {canWrite && current && (
            <div>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() => resume.mutate(a.availability_version)}
              >
                {current.status === "active" ? "Mở bán lại ngay" : "Hủy lịch tạm nghỉ"}
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {editable ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">
              {current ? "Sửa lịch tạm nghỉ" : "Đặt lịch tạm nghỉ"}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <form
              className="flex flex-col gap-3"
              onSubmit={(e) => {
                e.preventDefault();
                setError(null);
                save.mutate(a.availability_version);
              }}
            >
              <p className="text-xs text-muted-foreground">Giờ Việt Nam (UTC+7).</p>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="flex flex-col gap-1">
                  <Label htmlFor="vacation-start">Bắt đầu (để trống = ngay bây giờ)</Label>
                  <Input
                    id="vacation-start"
                    type="datetime-local"
                    value={values.start}
                    disabled={current?.status === "active" || !a.enabled}
                    onChange={(e) => setForm({ ...values, start: e.target.value })}
                  />
                </div>
                <div className="flex flex-col gap-1">
                  <Label htmlFor="vacation-end">Mở bán lại (để trống = tự mở bằng tay)</Label>
                  <Input
                    id="vacation-end"
                    type="datetime-local"
                    value={values.end}
                    disabled={!a.enabled}
                    onChange={(e) => setForm({ ...values, end: e.target.value })}
                  />
                </div>
              </div>
              <div className="flex flex-col gap-1">
                <Label htmlFor="vacation-message">Lời nhắn cho khách (tối đa 500 ký tự)</Label>
                <Textarea
                  id="vacation-message"
                  maxLength={500}
                  value={values.message}
                  disabled={!a.enabled}
                  onChange={(e) => setForm({ ...values, message: e.target.value })}
                />
              </div>
              {!a.enabled && (
                <p className="text-xs text-muted-foreground">
                  Sàn đang tạm dừng việc đặt lịch mới; bạn vẫn có thể mở bán lại hoặc hủy lịch.
                </p>
              )}
              <div>
                <Button type="submit" disabled={busy || !a.enabled}>
                  {save.isPending ? "Đang lưu…" : "Lưu lịch tạm nghỉ"}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      ) : (
        !canWrite && (
          <p className="text-sm text-muted-foreground">
            Bạn chỉ xem được trạng thái; cần quyền “Bật/tắt trạng thái bán” để thay đổi.
          </p>
        )
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}
