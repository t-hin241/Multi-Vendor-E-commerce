"use client";

import Link from "next/link";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuth } from "@/lib/auth-context";
import { resendEmailVerification } from "@/lib/api-client";
import * as api from "@/lib/account";
import { describeApiError } from "@/lib/errors";

export default function AccountPage() {
  const { user, isReady, callWithAuth } = useAuth();
  const capability = useQuery({
    queryKey: ["account-capabilities", user?.id],
    queryFn: () => callWithAuth(api.accountCapabilities),
    enabled: !!user,
    retry: false,
  });
  if (!isReady) return <p className="p-8">Đang tải…</p>;
  if (!user)
    return (
      <section className="p-8">
        <Link href="/login">Đăng nhập để quản lý tài khoản</Link>
      </section>
    );
  if (capability.isError)
    return (
      <section className="p-8">
        <p role="alert">Không thể kiểm tra tính năng tài khoản.</p>
        <Button onClick={() => void capability.refetch()}>Thử lại</Button>
      </section>
    );
  if (!capability.data) return <p className="p-8">Đang tải…</p>;
  if (!capability.data.account_self_service)
    return <p className="p-8">Quản lý tài khoản chưa được mở.</p>;
  return <Account key={user.id} userID={user.id} />;
}

function Account({ userID }: { userID: string }) {
  const { callWithAuth, updateUser, forgetSession } = useAuth();
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [cursor, setCursor] = useState<string>();
  const profile = useQuery({
    queryKey: ["account-profile", userID],
    queryFn: () => callWithAuth(api.accountProfile),
    retry: false,
  });
  const sessions = useQuery({
    queryKey: ["account-sessions", userID, cursor],
    queryFn: () => callWithAuth((t) => api.accountSessions(t, cursor)),
    retry: false,
  });
  async function run(action: () => Promise<void>) {
    setPending(true);
    setError("");
    setMessage("");
    try {
      await action();
    } catch (e) {
      setError(describeApiError(e, "Không thể thực hiện. Vui lòng thử lại."));
    } finally {
      setPending(false);
    }
  }
  function signedOut() {
    forgetSession();
    router.replace("/login");
  }
  return (
    <section className="mx-auto max-w-2xl space-y-8 px-4 py-10">
      <h1 className="text-2xl font-semibold">Tài khoản của tôi</h1>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {message && <p role="status">{message}</p>}
      {profile.isError ? (
        <>
          <p role="alert">Không thể tải hồ sơ.</p>
          <Button onClick={() => void profile.refetch()}>Tải lại hồ sơ</Button>
        </>
      ) : profile.data ? (
        <>
          <form
            key={profile.data.profile_version}
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault();
              const data = new FormData(e.currentTarget);
              void run(async () => {
                const result = await callWithAuth((t) =>
                  api.saveProfile(t, {
                    full_name: String(data.get("full_name")),
                    phone: String(data.get("phone")),
                    expected_version: profile.data!.profile_version,
                  }),
                );
                updateUser(result);
                await profile.refetch();
                setMessage("Đã lưu hồ sơ.");
              });
            }}
          >
            <h2 className="text-lg font-medium">Hồ sơ</h2>
            <Label htmlFor="full_name">Họ tên</Label>
            <Input
              id="full_name"
              name="full_name"
              defaultValue={profile.data.full_name}
              required
              maxLength={200}
            />
            <Label htmlFor="phone">Số điện thoại</Label>
            <Input
              id="phone"
              name="phone"
              type="tel"
              defaultValue={profile.data.phone}
              pattern="\+?[0-9]{7,15}"
              placeholder="Có thể để trống"
            />
            <Button disabled={pending}>Lưu hồ sơ</Button>{" "}
            <Button
              type="button"
              variant="outline"
              disabled={pending}
              onClick={() => void profile.refetch()}
            >
              Tải lại hồ sơ
            </Button>
          </form>
          <div className="space-y-3">
            <h2 className="text-lg font-medium">Email đăng nhập</h2>
            <p>{profile.data.email}</p>
            <p>{profile.data.email_verified ? "Đã xác minh" : "Chưa xác minh"}</p>
            {!profile.data.email_verified && (
              <Button
                disabled={pending}
                onClick={() =>
                  void run(async () => {
                    await callWithAuth(resendEmailVerification);
                    setMessage(
                      "Đã yêu cầu gửi liên kết xác minh. Kiểm tra hộp thư; gửi lại sau ít nhất một phút.",
                    );
                  })
                }
              >
                Gửi email xác minh
              </Button>
            )}
          </div>
        </>
      ) : (
        <p>Đang tải hồ sơ…</p>
      )}
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const data = new FormData(form);
          void run(async () => {
            try {
              await callWithAuth((t) =>
                api.changePassword(
                  t,
                  String(data.get("current_password")),
                  String(data.get("new_password")),
                ),
              );
              signedOut();
            } finally {
              form.reset();
            }
          });
        }}
      >
        <h2 className="text-lg font-medium">Đổi mật khẩu</h2>
        <p className="text-sm text-muted-foreground">Bạn sẽ cần đăng nhập lại trên mọi thiết bị.</p>
        <Label htmlFor="password-current">Mật khẩu hiện tại</Label>
        <Input
          id="password-current"
          name="current_password"
          type="password"
          autoComplete="current-password"
          required
          maxLength={72}
        />
        <Label htmlFor="password-new">Mật khẩu mới</Label>
        <Input
          id="password-new"
          name="new_password"
          type="password"
          autoComplete="new-password"
          required
          minLength={8}
          maxLength={72}
        />
        <Button disabled={pending}>Đổi mật khẩu và đăng xuất</Button>
      </form>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const data = new FormData(form);
          void run(async () => {
            try {
              await callWithAuth((t) =>
                api.changeEmail(
                  t,
                  String(data.get("new_email")),
                  String(data.get("current_password")),
                ),
              );
              setMessage(
                "Kiểm tra địa chỉ email mới để xác nhận. Email đăng nhập chỉ đổi sau khi xác nhận thành công.",
              );
            } finally {
              form.reset();
            }
          });
        }}
      >
        <h2 className="text-lg font-medium">Đổi email</h2>
        <p className="text-sm text-muted-foreground">
          Sau khi xác nhận, mọi phiên sẽ đăng xuất và địa chỉ cũ nhận thông báo.
        </p>
        <Label htmlFor="email-new">Email mới</Label>
        <Input
          id="email-new"
          name="new_email"
          type="email"
          autoComplete="email"
          required
          maxLength={254}
        />
        <Label htmlFor="email-password">Mật khẩu xác nhận đổi email</Label>
        <Input
          id="email-password"
          name="current_password"
          type="password"
          autoComplete="current-password"
          required
          maxLength={72}
        />
        <Button disabled={pending}>Gửi xác nhận đổi email</Button>
      </form>
      <div className="space-y-3">
        <h2 className="text-lg font-medium">Phiên đăng nhập</h2>
        <p className="text-sm text-muted-foreground">
          Thời gian hoạt động được ước tính từ lần gia hạn phiên gần nhất. Chưa có thông tin thiết
          bị.
        </p>
        {sessions.isError && (
          <>
            <p role="alert">Không thể tải phiên.</p>
            <Button onClick={() => void sessions.refetch()}>Thử lại</Button>
          </>
        )}
        {sessions.data?.items.map((s) => (
          <div key={s.id} className="flex items-center justify-between gap-4 rounded border p-3">
            <div>
              <p>{s.current ? "Phiên hiện tại" : "Phiên khác"}</p>
              <p className="text-sm">
                Bắt đầu:{" "}
                {new Date(s.created_at).toLocaleString("vi-VN", { timeZone: "Asia/Saigon" })}
              </p>
              <p className="text-sm">
                Hoạt động:{" "}
                {new Date(s.last_active_at).toLocaleString("vi-VN", { timeZone: "Asia/Saigon" })}
              </p>
            </div>
            <Button
              variant="outline"
              disabled={pending}
              onClick={() =>
                void run(async () => {
                  const result = await callWithAuth((t) => api.revokeSession(t, s.id));
                  if (result.sign_in_required) signedOut();
                  else await sessions.refetch();
                })
              }
            >
              Thu hồi{s.current ? " phiên này" : ""}
            </Button>
          </div>
        ))}
        {cursor && (
          <Button variant="outline" disabled={pending} onClick={() => setCursor(undefined)}>
            Về trang đầu
          </Button>
        )}
        {sessions.data?.next_cursor && (
          <Button
            variant="outline"
            disabled={pending}
            onClick={() => setCursor(sessions.data!.next_cursor)}
          >
            Trang tiếp
          </Button>
        )}
        <Button
          variant="destructive"
          disabled={pending}
          onClick={() =>
            void run(async () => {
              await callWithAuth(api.revokeAllSessions);
              signedOut();
            })
          }
        >
          Đăng xuất tất cả thiết bị
        </Button>
      </div>
    </section>
  );
}
