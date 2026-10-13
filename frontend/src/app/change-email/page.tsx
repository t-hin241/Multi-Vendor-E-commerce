"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { confirmEmailChange } from "@/lib/account";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

export default function ChangeEmailPage() {
  const token = useRef("");
  const [hasToken, setHasToken] = useState<boolean | null>(null);
  const [pending, setPending] = useState(false);
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const { forgetSession } = useAuth();
  useEffect(() => {
    if (window.location.hash) {
      token.current = new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "";
      window.history.replaceState(null, "", window.location.pathname);
    }
    const present = token.current !== "";
    queueMicrotask(() => setHasToken(present));
  }, []);
  async function confirm() {
    setPending(true);
    setError("");
    try {
      await confirmEmailChange(token.current);
      token.current = "";
      forgetSession();
      setDone(true);
    } catch (e) {
      setError(describeApiError(e, "Liên kết không hợp lệ hoặc đã hết hạn."));
    } finally {
      setPending(false);
    }
  }
  return (
    <section className="mx-auto max-w-md space-y-4 px-4 py-16">
      <h1 className="text-xl font-semibold">Xác nhận email đăng nhập mới</h1>
      {hasToken === null ? (
        <p>Đang tải…</p>
      ) : done ? (
        <>
          <p>Email đã thay đổi. Hãy đăng nhập lại bằng địa chỉ mới.</p>
          <Link href="/login">Đăng nhập</Link>
        </>
      ) : hasToken ? (
        <>
          <p>Xác nhận sẽ đổi email đăng nhập và thu hồi mọi phiên đang mở.</p>
          <Button disabled={pending} onClick={() => void confirm()}>
            {pending ? "Đang xác nhận…" : "Xác nhận đổi email"}
          </Button>
        </>
      ) : (
        <p>Liên kết thiếu mã xác nhận. Hãy yêu cầu liên kết mới từ trang tài khoản.</p>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
