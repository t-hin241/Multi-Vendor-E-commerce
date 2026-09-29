"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent } from "@/components/ui/card";
import { useAuth } from "@/lib/auth-context";
import * as ops from "@/lib/vendor-operations";

export function PayoutAccounts({ vendorId, admin = false }: { vendorId: string; admin?: boolean }) {
  const { callWithAuth } = useAuth();
  const cache = useQueryClient();
  const [offset, setOffset] = useState(0);
  const [form, setForm] = useState({ bank_bin: "", account_number: "", account_name: "" });
  const [reason, setReason] = useState("");
  const [details, setDetails] = useState<ops.PayoutDetails | null>(null);
  useEffect(() => {
    if (!details) return;
    const timer = setTimeout(() => setDetails(null), 60000);
    return () => clearTimeout(timer);
  }, [details]);
  const key = ["payout-accounts", vendorId, admin, offset];
  const query = useQuery({
    queryKey: key,
    queryFn: () => callWithAuth((t) => ops.listAccounts(t, vendorId, admin, offset)),
  });
  const mutation = useMutation({
    mutationFn: async (action: {
      account?: ops.PayoutAccount;
      verify?: boolean;
      read?: boolean;
    }) => {
      if (action.read && action.account) {
        setDetails(
          await callWithAuth((t) => ops.accountDetails(t, vendorId, action.account!, reason)),
        );
        return;
      }
      if (action.account)
        await callWithAuth((t) =>
          ops.decideAccount(t, vendorId, action.account!, Boolean(action.verify), reason),
        );
      else {
        await callWithAuth((t) => ops.submitAccount(t, vendorId, form));
        setForm({ bank_bin: "", account_number: "", account_name: "" });
      }
      setDetails(null);
      setReason("");
      await cache.invalidateQueries({ queryKey: ["payout-accounts", vendorId] });
    },
  });
  return (
    <div className="space-y-4">
      <h2 className="font-semibold">
        {admin ? "Payout account verification" : "Tài khoản nhận tiền"}
      </h2>
      <p className="text-sm text-muted-foreground">
        {admin
          ? "Review bank evidence before verifying. Pending payouts keep their original destination version."
          : "Tài khoản mới cần được xác minh. Tài khoản đã xác minh trước đó vẫn dùng cho các khoản thanh toán đang xử lý."}
      </p>
      {!admin && (
        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault();
            mutation.mutate({});
          }}
        >
          <label className="block text-sm">
            Mã BIN ngân hàng
            <Input
              inputMode="numeric"
              pattern="[0-9]{6}"
              maxLength={6}
              required
              value={form.bank_bin}
              onChange={(e) => setForm({ ...form, bank_bin: e.target.value })}
            />
          </label>
          <label className="block text-sm">
            Số tài khoản
            <Input
              autoComplete="off"
              inputMode="numeric"
              pattern="[0-9]{6,32}"
              maxLength={32}
              required
              value={form.account_number}
              onChange={(e) => setForm({ ...form, account_number: e.target.value })}
            />
          </label>
          <label className="block text-sm">
            Tên chủ tài khoản
            <Input
              autoComplete="off"
              minLength={2}
              maxLength={160}
              required
              value={form.account_name}
              onChange={(e) => setForm({ ...form, account_name: e.target.value })}
            />
          </label>
          <Button disabled={mutation.isPending}>Gửi xác minh</Button>
        </form>
      )}
      {admin && (
        <label className="block text-sm">
          Review evidence / reason (do not include account numbers)
          <Input
            minLength={2}
            maxLength={200}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>
      )}
      {(query.error || mutation.error) && (
        <p role="alert" className="text-sm text-destructive">
          {(query.error || mutation.error)?.message}
        </p>
      )}
      {query.isPending && <p>{admin ? "Loading…" : "Đang tải…"}</p>}
      {details && (
        <Card>
          <CardContent className="space-y-2">
            <p>
              {details.bank_bin} · {details.account_number}
            </p>
            <p>{details.account_name}</p>
            <Button variant="outline" onClick={() => setDetails(null)}>
              Hide details
            </Button>
          </CardContent>
        </Card>
      )}
      {query.data?.map((a) => (
        <Card key={a.id}>
          <CardContent className="space-y-2">
            <p>
              {a.bank_bin} · ****{a.last4} · v{a.version} · {a.status}
              {a.is_default ? (admin ? " · Default" : " · Mặc định") : ""}
            </p>
            {a.rejection_reason && <p className="text-sm">{a.rejection_reason}</p>}
            {admin && (
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="outline"
                  disabled={mutation.isPending || reason.trim().length < 2}
                  onClick={() => mutation.mutate({ account: a, read: true })}
                >
                  View details (audited)
                </Button>
                {a.status === "pending" && (
                  <>
                    <Button
                      disabled={mutation.isPending || reason.trim().length < 2}
                      onClick={() => mutation.mutate({ account: a, verify: true })}
                    >
                      Verify
                    </Button>
                    <Button
                      variant="destructive"
                      disabled={mutation.isPending || reason.trim().length < 2}
                      onClick={() => mutation.mutate({ account: a, verify: false })}
                    >
                      Reject
                    </Button>
                  </>
                )}
              </div>
            )}
          </CardContent>
        </Card>
      ))}
      {query.data?.length === 0 && (
        <p className="text-sm">{admin ? "No payout accounts." : "Chưa có tài khoản nhận tiền."}</p>
      )}
      <div className="flex gap-2">
        <Button
          variant="outline"
          disabled={offset === 0 || query.isFetching}
          onClick={() => setOffset(Math.max(0, offset - 20))}
        >
          {admin ? "Previous" : "Trước"}
        </Button>
        <Button
          variant="outline"
          disabled={query.data?.length !== 20 || query.isFetching}
          onClick={() => setOffset(offset + 20)}
        >
          {admin ? "Next" : "Sau"}
        </Button>
      </div>
    </div>
  );
}
