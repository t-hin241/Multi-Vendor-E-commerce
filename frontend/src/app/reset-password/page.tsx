"use client";
import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { confirmPasswordReset } from "@/lib/api-client";
import { describeApiError } from "@/lib/errors";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export default function ResetPasswordPage() {
  const token = useRef("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [pending, setPending] = useState(false);
  const [done, setDone] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    if (window.location.hash) {
      token.current = new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "";
      window.history.replaceState(null, "", window.location.pathname);
    }
  }, []);
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!token.current) {
      setMessage("Liên kết thiếu mã đặt lại. Vui lòng yêu cầu liên kết mới.");
      return;
    }
    if (password !== confirm) {
      setMessage("Mật khẩu xác nhận không khớp.");
      return;
    }
    if (new TextEncoder().encode(password).length > 72) {
      setMessage("Mật khẩu không được vượt quá 72 byte.");
      return;
    }
    setPending(true);
    try {
      await confirmPasswordReset(token.current, password);
      token.current = "";
      setPassword("");
      setConfirm("");
      setDone(true);
      setMessage("Đã đặt lại mật khẩu và thu hồi các phiên cũ. Hãy đăng nhập lại.");
    } catch (err) {
      setMessage(
        describeApiError(
          err,
          "Liên kết không hợp lệ hoặc đã hết hạn. Vui lòng yêu cầu liên kết mới.",
        ),
      );
    } finally {
      setPending(false);
    }
  }
  return (
    <section className="mx-auto max-w-sm space-y-4 px-4 py-16">
      <h1 className="text-xl font-semibold">Đặt lại mật khẩu</h1>
      {!done && (
        <form onSubmit={submit} className="space-y-4">
          <label htmlFor="password">Mật khẩu mới</label>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            minLength={8}
            maxLength={72}
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <label htmlFor="confirm">Nhập lại mật khẩu</label>
          <Input
            id="confirm"
            type="password"
            autoComplete="new-password"
            minLength={8}
            maxLength={72}
            required
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />
          <Button type="submit" disabled={pending}>
            {pending ? "Đang cập nhật…" : "Đổi mật khẩu"}
          </Button>
        </form>
      )}
      <p role="status">{message}</p>
      <div className="flex gap-4">
        <Link className="underline" href="/forgot-password">
          Yêu cầu liên kết mới
        </Link>
        <Link className="underline" href="/login">
          Đăng nhập
        </Link>
      </div>
    </section>
  );
}
