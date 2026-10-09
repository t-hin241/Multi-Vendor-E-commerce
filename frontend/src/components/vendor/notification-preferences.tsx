"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { queryKeys } from "@/lib/query-keys";
import {
  noticesDisabled,
  preferencesErrorMessage,
  toggleCategory,
  VENDOR_NOTICE_CATEGORIES,
} from "@/lib/vendor-notices";

// NotificationPreferences lets a shop member choose which work emails they
// receive (AF-08). The owner always receives every category; staff receive
// a category only if they opted in and Vendor says they hold the matching
// permission when the email is prepared. Hidden while the feature is off.
export function NotificationPreferences({ owner }: { owner: boolean }) {
  const { callWithAuth, user } = useAuth();
  const queryClient = useQueryClient();
  const key = queryKeys.notificationPreferences(user?.id ?? "");
  const prefs = useQuery({
    queryKey: key,
    queryFn: () => callWithAuth((token) => api.getNotificationPreferences(token)),
    retry: false,
  });
  const [draft, setDraft] = useState<api.VendorNoticeCategory[] | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  if (prefs.isPending || noticesDisabled(prefs.error)) return null;
  if (prefs.data && !prefs.data.vendor_notices_enabled) return null;
  if (prefs.isError) {
    return <p className="text-sm text-destructive">Chưa tải được tùy chọn thông báo.</p>;
  }
  const current = draft ?? prefs.data.optional_vendor_categories;
  const changed = draft !== null;

  async function save() {
    setSaving(true);
    setError(null);
    setSaved(false);
    try {
      const next = await callWithAuth((token) =>
        api.updateNotificationPreferences(
          token,
          { optional_vendor_categories: current },
          prefs.data!.version,
        ),
      );
      queryClient.setQueryData(key, next);
      setDraft(null);
      setSaved(true);
    } catch (err) {
      setError(preferencesErrorMessage(err));
      if (err instanceof api.ApiError && err.status === 409) {
        await queryClient.invalidateQueries({ queryKey: key });
        setDraft(null);
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Email công việc của shop</CardTitle>
        <CardDescription>
          {owner
            ? "Bạn là chủ shop nên luôn nhận mọi email công việc. Lựa chọn dưới đây chỉ áp dụng khi bạn là nhân viên ở shop khác."
            : "Chọn loại việc bạn muốn nhận email. Bạn chỉ nhận email cho loại việc mà chủ shop đã cấp quyền."}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {VENDOR_NOTICE_CATEGORIES.map((c) => (
          <Label key={c.value} className="flex items-start gap-2 text-sm font-normal">
            <Checkbox
              checked={current.includes(c.value)}
              onCheckedChange={(on) => {
                setSaved(false);
                setDraft(toggleCategory(current, c.value, on === true));
              }}
            />
            <span>
              <span className="font-medium">{c.label}</span>
              <span className="block text-xs text-muted-foreground">{c.hint}</span>
            </span>
          </Label>
        ))}
        <p className="text-xs text-muted-foreground">
          Email chỉ ghi mã đơn và đường dẫn vào trang quản lý; không chứa địa chỉ người mua, số tiền
          hay số tài khoản.
        </p>
        {error && <p className="text-sm text-destructive">{error}</p>}
        {saved && <p className="text-sm text-muted-foreground">Đã lưu.</p>}
        <Button size="sm" disabled={!changed || saving} onClick={save}>
          {saving ? "Đang lưu…" : "Lưu"}
        </Button>
      </CardContent>
    </Card>
  );
}
