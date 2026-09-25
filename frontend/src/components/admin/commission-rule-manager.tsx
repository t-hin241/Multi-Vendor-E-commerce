"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// CommissionRuleManager lets admin set the marketplace's commission
// percentage. Rules are insert-only on the backend — setting a new one
// never edits history, so past vendor orders keep the rate they were
// snapshotted with when paid.
export function CommissionRuleManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [percent, setPercent] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const rulesQuery = useQuery({
    queryKey: ["commission-rules"],
    queryFn: () => callWithAuth((token) => api.listCommissionRules(token)),
  });

  const current = rulesQuery.data?.[0];
  const loadError = rulesQuery.error
    ? rulesQuery.error instanceof api.ApiError
      ? rulesQuery.error.message
      : "Could not load commission rules."
    : null;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const percentValue = Number(percent);
    if (Number.isNaN(percentValue) || percentValue < 0 || percentValue > 100) {
      setError("Enter a percentage between 0 and 100.");
      return;
    }
    setIsSubmitting(true);
    try {
      await callWithAuth((token) => api.setCommissionRule(token, Math.round(percentValue * 100)));
      setPercent("");
      await queryClient.invalidateQueries({ queryKey: ["commission-rules"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update the commission rate.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader title="Commission" />

      <Card>
        <CardContent className="flex flex-col gap-3">
          {current ? (
            <p className="text-sm text-muted-foreground">
              Current rate: <span className="font-medium text-foreground">{(current.rate_bps / 100).toFixed(2)}%</span>
            </p>
          ) : (
            !loadError && <p className="text-sm text-muted-foreground">No commission rule set yet.</p>
          )}
          {loadError && <p className="text-sm text-destructive">{loadError}</p>}

          <form onSubmit={handleSubmit} className="flex items-end gap-3">
            <Label className="flex flex-col items-start gap-1.5 text-sm">
              New rate (%)
              <Input
                type="number"
                step="0.01"
                min={0}
                max={100}
                value={percent}
                onChange={(e) => setPercent(e.target.value)}
                required
                className="w-32"
              />
            </Label>
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? "Saving…" : "Set rate"}
            </Button>
          </form>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
      </Card>
    </div>
  );
}
