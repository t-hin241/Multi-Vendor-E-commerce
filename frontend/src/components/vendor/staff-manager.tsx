"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { describeApiError } from "@/lib/errors";
import type { ConsoleShop } from "@/lib/hooks/use-console-shops";
import { queryKeys } from "@/lib/query-keys";
import { grantablePermissions, permissionLabel } from "@/lib/shop-access";

const INVITATION_STATUS: Record<api.StaffInvitation["status"], string> = {
  pending: "Đang chờ nhận",
  accepted: "Đã nhận",
  revoked: "Đã thu hồi",
  superseded: "Đã thay bằng lời mời mới",
};

function PermissionPicker({
  options,
  value,
  onChange,
  idPrefix,
}: {
  options: api.PermissionDefinition[];
  value: string[];
  onChange: (next: string[]) => void;
  idPrefix: string;
}) {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {options.map((p) => {
        const id = `${idPrefix}-${p.name}`;
        return (
          <div key={p.name} className="flex items-center gap-2">
            <Checkbox
              id={id}
              checked={value.includes(p.name)}
              onCheckedChange={(checked) =>
                onChange(checked ? [...value, p.name] : value.filter((v) => v !== p.name))
              }
            />
            <Label htmlFor={id} className="text-sm font-normal">
              {permissionLabel(p.name)}
            </Label>
          </div>
        );
      })}
    </div>
  );
}

function MemberRow({
  shop,
  member,
  options,
  self,
  onChanged,
}: {
  shop: ConsoleShop;
  member: api.ShopMember;
  options: api.PermissionDefinition[];
  self: boolean;
  onChanged: () => Promise<void>;
}) {
  const { callWithAuth } = useAuth();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(member.permissions);
  const [busy, setBusy] = useState(false);
  const staff = member.role === "staff" && member.status === "active";
  // A manager only touches members whose permissions it holds itself; the
  // backend refuses the rest, this just avoids offering it.
  const manageable =
    staff && !self && member.permissions.every((p) => shop.capabilities.includes(p));

  async function save() {
    setBusy(true);
    try {
      await callWithAuth((t) =>
        api.updateShopMember(t, shop.id, member.user_id, draft, member.version),
      );
      toast.success("Đã cập nhật quyền.");
      setEditing(false);
      await onChanged();
    } catch (err) {
      toast.error(describeApiError(err, "Không cập nhật được quyền."));
      await onChanged();
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    const question = self
      ? "Rời cửa hàng này? Bạn sẽ mất quyền truy cập ngay."
      : "Thu hồi quyền của người này? Họ sẽ bị chặn ngay ở thao tác tiếp theo.";
    if (!window.confirm(question)) return;
    setBusy(true);
    try {
      await callWithAuth((t) => api.removeShopMember(t, shop.id, member.user_id, member.version));
      toast.success(self ? "Bạn đã rời cửa hàng." : "Đã thu hồi quyền.");
      await onChanged();
    } catch (err) {
      toast.error(describeApiError(err, "Không thu hồi được quyền."));
      await onChanged();
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="flex flex-col gap-2 border-b py-3 last:border-b-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{member.email || member.user_id}</span>
        <Badge variant={member.role === "owner" ? "default" : "outline"}>
          {member.role === "owner" ? "Chủ shop" : "Nhân viên"}
        </Badge>
        {member.status === "revoked" && <Badge variant="secondary">Đã thu hồi</Badge>}
        {self && <span className="text-xs text-muted-foreground">(bạn)</span>}
        <span className="ml-auto flex gap-2">
          {manageable && !editing && (
            <Button size="sm" variant="outline" disabled={busy} onClick={() => setEditing(true)}>
              Sửa quyền
            </Button>
          )}
          {staff && (manageable || self) && (
            <Button size="sm" variant="ghost" disabled={busy} onClick={remove}>
              {self ? "Rời cửa hàng" : "Thu hồi"}
            </Button>
          )}
        </span>
      </div>
      {member.role === "owner" ? (
        <p className="text-xs text-muted-foreground">Toàn quyền, gồm tài khoản nhận tiền.</p>
      ) : editing ? (
        <div className="flex flex-col gap-2">
          <PermissionPicker
            options={options}
            value={draft}
            onChange={setDraft}
            idPrefix={`member-${member.user_id}`}
          />
          <div className="flex gap-2">
            <Button size="sm" disabled={busy || draft.length === 0} onClick={save}>
              Lưu
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setDraft(member.permissions);
                setEditing(false);
              }}
            >
              Hủy
            </Button>
          </div>
        </div>
      ) : (
        member.status === "active" && (
          <p className="text-xs text-muted-foreground">
            {member.permissions.map(permissionLabel).join(" · ")}
          </p>
        )
      )}
    </li>
  );
}

// StaffManager lets an owner (or staff with staff.manage) invite people by
// email, change or revoke their permissions (AF-17). Grants are limited to
// what the manager holds; Vendor enforces the same rules.
export function StaffManager({ shop }: { shop: ConsoleShop }) {
  const { callWithAuth, user } = useAuth();
  const queryClient = useQueryClient();
  const [email, setEmail] = useState("");
  const [permissions, setPermissions] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  const registry = useQuery({
    queryKey: queryKeys.staffPermissions(),
    queryFn: () => callWithAuth((t) => api.listStaffPermissions(t)),
    staleTime: 5 * 60_000,
  });
  const members = useQuery({
    queryKey: queryKeys.shopMembers(shop.id),
    queryFn: () => callWithAuth((t) => api.listShopMembers(t, shop.id)),
  });
  const invitations = useQuery({
    queryKey: queryKeys.staffInvitations(shop.id),
    queryFn: () => callWithAuth((t) => api.listStaffInvitations(t, shop.id)),
  });
  const options = grantablePermissions(registry.data?.permissions ?? [], shop);
  const enabled = registry.data?.enabled ?? false;

  async function refresh() {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.shopMembers(shop.id) }),
      queryClient.invalidateQueries({ queryKey: queryKeys.staffInvitations(shop.id) }),
      queryClient.invalidateQueries({ queryKey: queryKeys.accessibleShops() }),
    ]);
  }

  async function invite(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await callWithAuth((t) => api.inviteStaff(t, shop.id, email.trim(), permissions));
      toast.success("Đã gửi lời mời. Người nhận cần đăng nhập bằng đúng email này để chấp nhận.");
      setEmail("");
      setPermissions([]);
      await refresh();
    } catch (err) {
      toast.error(describeApiError(err, "Không gửi được lời mời."));
    } finally {
      setBusy(false);
    }
  }

  async function revoke(invitation: api.StaffInvitation) {
    if (!window.confirm(`Thu hồi lời mời gửi ${invitation.email_hint}?`)) return;
    try {
      await callWithAuth((t) => api.revokeStaffInvitation(t, shop.id, invitation.id));
      toast.success("Đã thu hồi lời mời.");
    } catch (err) {
      toast.error(describeApiError(err, "Không thu hồi được lời mời."));
    }
    await refresh();
  }

  const pending = (invitations.data ?? []).filter((i) => i.status === "pending");
  const history = (invitations.data ?? []).filter((i) => i.status !== "pending").slice(0, 10);

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardContent className="flex flex-col gap-3 pt-6">
          <h2 className="font-medium">Mời nhân viên</h2>
          {!enabled ? (
            <p className="text-sm text-muted-foreground">
              Tính năng nhân viên cửa hàng chưa được bật trên hệ thống.
            </p>
          ) : (
            <form className="flex flex-col gap-3" onSubmit={invite}>
              <div className="flex flex-col gap-1">
                <Label htmlFor="staff-email">Email người được mời</Label>
                <Input
                  id="staff-email"
                  type="email"
                  autoComplete="off"
                  required
                  maxLength={254}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Lời mời có hạn 7 ngày và chỉ dùng được một lần, bởi tài khoản có đúng email này.
                  Không chia sẻ mật khẩu của bạn.
                </p>
              </div>
              <PermissionPicker
                options={options}
                value={permissions}
                onChange={setPermissions}
                idPrefix="invite"
              />
              <Button
                type="submit"
                className="self-start"
                disabled={busy || !email.trim() || permissions.length === 0}
              >
                Gửi lời mời
              </Button>
            </form>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardContent className="pt-6">
          <h2 className="font-medium">Thành viên</h2>
          {members.isPending && <p className="text-sm text-muted-foreground">Đang tải…</p>}
          {members.isError && (
            <p className="text-sm text-destructive">
              {describeApiError(members.error, "Không tải được danh sách thành viên.")}
            </p>
          )}
          <ul>
            {(members.data ?? []).map((m) => (
              <MemberRow
                key={`${m.user_id}-${m.version}`}
                shop={shop}
                member={m}
                options={options}
                self={m.user_id === user?.id}
                onChanged={refresh}
              />
            ))}
          </ul>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="flex flex-col gap-2 pt-6">
          <h2 className="font-medium">Lời mời</h2>
          {pending.length === 0 && (
            <p className="text-sm text-muted-foreground">Không có lời mời đang chờ.</p>
          )}
          <ul className="flex flex-col gap-2">
            {pending.map((i) => (
              <li key={i.id} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-medium">{i.email_hint}</span>
                <Badge variant="outline">
                  {i.expired ? "Hết hạn" : INVITATION_STATUS[i.status]}
                </Badge>
                {i.delivery_status === "parked" && (
                  <Badge variant="secondary">Chưa gửi được email</Badge>
                )}
                <span className="text-xs text-muted-foreground">
                  {i.permissions.map(permissionLabel).join(" · ")}
                </span>
                <Button size="sm" variant="ghost" className="ml-auto" onClick={() => revoke(i)}>
                  Thu hồi
                </Button>
              </li>
            ))}
          </ul>
          {history.length > 0 && (
            <details className="text-sm">
              <summary className="cursor-pointer text-muted-foreground">Lời mời trước đây</summary>
              <ul className="mt-2 flex flex-col gap-1">
                {history.map((i) => (
                  <li key={i.id} className="text-xs text-muted-foreground">
                    {i.email_hint} — {INVITATION_STATUS[i.status]}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
