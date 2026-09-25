"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ConfirmDialog } from "@/components/admin/confirm-dialogs";
import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { useDebouncedValue } from "@/lib/use-debounced-value";

const ROLE_OPTIONS = ["", "buyer", "vendor", "admin"] as const;

// UserModeration is admin's trust-and-safety tool: find an account and
// suspend or reactivate it. There's no reason field here (unlike vendor/
// product reject) — the backend endpoint doesn't take one, matching
// docs/upgrades/01-schema-display-gaps.md finding 3's own recommendation.
export function UserModeration() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [role, setRole] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const q = useDebouncedValue(searchInput, 400).trim();

  const usersQuery = useQuery({
    queryKey: ["admin-users", role, q],
    queryFn: () => callWithAuth((token) => api.listUsers(token, { role: role || undefined, q: q || undefined })),
  });

  async function handleSetActive(id: string, isActive: boolean) {
    await callWithAuth((token) => api.setUserActive(token, id, isActive));
    await queryClient.invalidateQueries({ queryKey: ["admin-users"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Users"
        action={<StatusFilter status={role} onChange={setRole} options={ROLE_OPTIONS} />}
      />

      <Input
        value={searchInput}
        onChange={(e) => setSearchInput(e.target.value)}
        placeholder="Search by name or email…"
        className="max-w-sm"
        aria-label="Search users"
      />

      {usersQuery.error && (
        <p className="text-sm text-destructive">
          Could not load users:{" "}
          {usersQuery.error instanceof api.ApiError ? usersQuery.error.message : "unknown error"}
        </p>
      )}

      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Full name</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {usersQuery.data?.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-medium whitespace-normal">{u.full_name}</TableCell>
                  <TableCell className="whitespace-normal text-muted-foreground">{u.email}</TableCell>
                  <TableCell className="capitalize">{u.role}</TableCell>
                  <TableCell>
                    <Badge variant={u.is_active ? "success" : "destructive"}>
                      {u.is_active ? "Active" : "Suspended"}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-right">
                    {u.role === "admin" ? (
                      <span className="text-xs text-muted-foreground">—</span>
                    ) : u.is_active ? (
                      <ConfirmDialog
                        trigger={
                          <Button size="sm" variant="outline" className="text-destructive">
                            Suspend
                          </Button>
                        }
                        title={`Suspend ${u.full_name}?`}
                        description="They will be signed out immediately and unable to log back in until reactivated."
                        confirmLabel="Suspend"
                        variant="destructive"
                        onConfirm={() => handleSetActive(u.id, false)}
                      />
                    ) : (
                      <ConfirmDialog
                        trigger={
                          <Button size="sm" variant="secondary">
                            Reactivate
                          </Button>
                        }
                        title={`Reactivate ${u.full_name}?`}
                        description="They will be able to log in again."
                        confirmLabel="Reactivate"
                        onConfirm={() => handleSetActive(u.id, true)}
                      />
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {usersQuery.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No users for this filter.
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
