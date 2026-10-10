"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { confirmEmailVerification } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";

// PW-022: the email link opens here with #token=... The fragment never
// reaches a server; it is read once, removed from the address bar and sent
// only in the request body when the person confirms.
export default function VerifyEmailPage() {
  const { user } = useAuth();
  const token = useRef("");
  const [hasToken, setHasToken] = useState<boolean | null>(null);
  const [pending, setPending] = useState(false);
  const [done, setDone] = useState(false);
  const [message, setMessage] = useState("");

  useEffect(() => {
    if (window.location.hash) {
      token.current = new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "";
      window.history.replaceState(null, "", window.location.pathname);
    }
    setHasToken(token.current !== "");
  }, []);

  async function confirm() {
    setPending(true);
    setMessage("");
    try {
      await confirmEmailVerification(token.current);
      token.current = "";
      setDone(true);
    } catch (err) {
      setMessage(
        describeApiError(
          err,
          "Liên kết không hợp lệ hoặc đã hết hạn. Hãy yêu cầu gửi lại email xác nhận.",
        ),
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="mx-auto max-w-sm space-y-4 px-4 py-16">
      <h1 className="text-xl font-semibold">Xác nhận địa chỉ email</h1>
      {hasToken === null ? (
        <p className="text-sm text-muted-foreground">Đang tải…</p>
      ) : done ? (
        <>
          <p className="text-sm">Địa chỉ email đã được xác nhận.</p>
          <Link href={user ? "/" : "/login"} className="text-sm text-primary underline">
            {user ? "Về trang chủ" : "Đăng nhập"}
          </Link>
        </>
      ) : !hasToken ? (
        <p className="text-sm text-muted-foreground">
          Liên kết thiếu mã xác nhận. Hãy mở lại đúng liên kết trong email, hoặc đăng nhập và yêu
          cầu gửi lại email xác nhận.
        </p>
      ) : (
        <>
          <p className="text-sm text-muted-foreground">
            Bấm xác nhận để chứng minh đây là địa chỉ email của bạn. Liên kết chỉ dùng được một lần.
          </p>
          <Button onClick={confirm} disabled={pending}>
            {pending ? "Đang xác nhận…" : "Xác nhận email"}
          </Button>
        </>
      )}
      {message && (
        <p role="alert" className="text-sm text-destructive">
          {message}
        </p>
      )}
    </section>
  );
}
