"use client";
import { useState } from "react";
import Link from "next/link";
import { requestPasswordReset } from "@/lib/api-client";
import { describeApiError } from "@/lib/errors";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setPending(true);
    setMessage("");
    try {
      await requestPasswordReset(email);
      setMessage(
        "Nếu email đã đăng ký, bạn sẽ nhận được liên kết đặt lại mật khẩu. Vui lòng kiểm tra hộp thư.",
      );
    } catch (err) {
      setMessage(describeApiError(err, "Không thể gửi yêu cầu. Vui lòng thử lại."));
    } finally {
      setPending(false);
    }
  }
  return (
    <section className="mx-auto max-w-sm space-y-4 px-4 py-16">
      <h1 className="text-xl font-semibold">Quên mật khẩu</h1>
      <form onSubmit={submit} className="space-y-4">
        <label htmlFor="email">Email</label>
        <Input
          id="email"
          type="email"
          autoComplete="email"
          required
          maxLength={254}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <Button disabled={pending} type="submit">
          {pending ? "Đang gửi…" : "Gửi liên kết đặt lại"}
        </Button>
      </form>
      <p role="status">{message}</p>
      <Link href="/login" className="underline">
        Quay lại đăng nhập
      </Link>
    </section>
  );
}
