"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { queryKeys } from "@/lib/query-keys";
import { noticesDisabled, preferencesErrorMessage } from "@/lib/vendor-notices";

// MarketingPreference (AF-09): the person's consent to promotional email.
// It never turns off notices about their orders, payments or shop work.
// Each change is recorded with its time on the server.
export function MarketingPreference() {
  const { user, callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const key = queryKeys.notificationPreferences(user?.id ?? "");
  const prefs = useQuery({
    queryKey: key,
    queryFn: () => callWithAuth((token) => api.getNotificationPreferences(token)),
    enabled: Boolean(user),
    retry: false,
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!prefs.data || noticesDisabled(prefs.error)) return null;

  async function change(on: boolean) {
    setSaving(true);
    setError(null);
    try {
      const next = await callWithAuth((token) =>
        api.updateNotificationPreferences(token, { marketing_opt_in: on }, prefs.data!.version),
      );
      queryClient.setQueryData(key, next);
    } catch (err) {
      setError(preferencesErrorMessage(err));
      await queryClient.invalidateQueries({ queryKey: key });
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card className="mt-6">
      <CardContent className="space-y-2 text-sm">
        <Label className="flex items-center justify-between gap-4 font-normal">
          <span>
            <span className="font-medium">Nhận email khuyến mãi</span>
            <span className="block text-xs text-muted-foreground">
              Thông báo về đơn hàng, thanh toán và công việc của shop vẫn luôn được gửi.
            </span>
          </span>
          <Switch
            checked={prefs.data.marketing_opt_in}
            disabled={saving}
            onCheckedChange={(on) => void change(on)}
          />
        </Label>
        {error && <p className="text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
