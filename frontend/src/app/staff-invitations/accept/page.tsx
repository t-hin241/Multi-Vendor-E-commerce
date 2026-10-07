"use client";

import { useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import { queryKeys } from "@/lib/query-keys";
import { acceptErrorMessage, permissionLabel, readInvitationToken } from "@/lib/shop-access";

// AF-17: the invitation email links here with #token=... The fragment never
// reaches a server; it is read once, removed from the address bar and sent
// only in the body of the accept request, after the person confirms.
export default function AcceptStaffInvitationPage() {
  const { user, isReady, callWithAuth, setSelectedVendorId } = useAuth();
  const queryClient = useQueryClient();
  const token = useRef<string | null>(null);
  const [hasToken, setHasToken] = useState<boolean | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [joined, setJoined] = useState<api.ShopMember | null>(null);

  useEffect(() => {
    token.current = readInvitationToken(window.location.hash);
    if (window.location.hash) {
      window.history.replaceState(null, "", window.location.pathname);
    }
    setHasToken(token.current !== null);
  }, []);

  async function accept() {
    if (!token.current) return;
    setPending(true);
    setError(null);
    try {
      const member = await callWithAuth((t) => api.acceptStaffInvitation(t, token.current!));
      token.current = null;
      setJoined(member);
      setSelectedVendorId(member.vendor_id);
      await queryClient.invalidateQueries({ queryKey: queryKeys.accessibleShops() });
    } catch (err) {
      setError(
        err instanceof api.ApiError && err.status > 0 && err.status < 500
          ? acceptErrorMessage(err.code, err.message)
          : describeApiError(err, "Chưa nhận được lời mời."),
      );
    } finally {
      setPending(false);
    }
  }

  let body: React.ReactNode;
  if (!isReady || hasToken === null) {
    body = <p className="text-sm text-muted-foreground">Đang tải…</p>;
  } else if (joined) {
    body = (
      <>
        <p className="text-sm">Bạn đã tham gia cửa hàng với các quyền:</p>
        <p className="text-sm text-muted-foreground">
          {joined.permissions.map(permissionLabel).join(" · ")}
        </p>
        <Button asChild className="self-start">
          <Link href="/vendor">Mở kênh người bán</Link>
        </Button>
      </>
    );
  } else if (!hasToken) {
    body = (
      <p className="text-sm text-muted-foreground">
        Liên kết thiếu hoặc sai mã mời. Hãy mở lại đúng liên kết trong email, hoặc xin cửa hàng gửi
        lời mời mới.
      </p>
    );
  } else if (!user) {
    body = (
      <>
        <p className="text-sm text-muted-foreground">
          Đăng nhập bằng đúng email đã nhận lời mời, rồi mở lại liên kết trong email.
        </p>
        <Button asChild className="self-start">
          <Link href="/login">Đăng nhập</Link>
        </Button>
      </>
    );
  } else if (user.role === "admin") {
    body = (
      <p className="text-sm text-muted-foreground">
        Tài khoản quản trị không thể tham gia cửa hàng. Hãy đăng nhập bằng tài khoản được mời.
      </p>
    );
  } else {
    body = (
      <>
        <p className="text-sm">
          Bạn đang đăng nhập là <span className="font-medium">{user.email}</span>. Lời mời chỉ dùng
          được nếu đây đúng là email được mời. Vai trò tài khoản của bạn không thay đổi.
        </p>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <Button className="self-start" disabled={pending} onClick={accept}>
          Chấp nhận lời mời
        </Button>
      </>
    );
  }

  return (
    <div className="mx-auto max-w-lg px-4 py-10">
      <Card>
        <CardContent className="flex flex-col gap-3 pt-6">
          <h1 className="text-lg font-semibold">Lời mời làm nhân viên cửa hàng</h1>
          {body}
        </CardContent>
      </Card>
    </div>
  );
}
