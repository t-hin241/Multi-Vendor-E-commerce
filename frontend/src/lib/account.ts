import { request, type User } from "@/lib/api-client";

export type AccountProfile = User & { phone: string; profile_version: number };
export type AccountSession = {
  id: string;
  created_at: string;
  last_active_at: string;
  current: boolean;
};
export const accountCapabilities = (token: string) =>
  request<{ account_self_service: boolean }>("/api/auth/account-capabilities", { token });
export const accountProfile = (token: string) => request<AccountProfile>("/api/auth/me", { token });
export const saveProfile = (
  token: string,
  profile: { full_name: string; phone: string; expected_version: number },
) => request<AccountProfile>("/api/auth/me", { token, method: "PATCH", json: profile });
export const changePassword = (token: string, current_password: string, new_password: string) =>
  request<{ sign_in_required: boolean }>("/api/auth/password-changes", {
    token,
    method: "POST",
    json: { current_password, new_password },
  });
export const changeEmail = (token: string, new_email: string, current_password: string) =>
  request<{ sent: boolean }>("/api/auth/email-changes", {
    token,
    method: "POST",
    json: { new_email, current_password },
  });
export const confirmEmailChange = (token: string) =>
  request<{ sign_in_required: boolean }>("/api/auth/email-changes/confirm", {
    method: "POST",
    json: { token },
  });
export const accountSessions = (token: string, cursor?: string) =>
  request<{ items: AccountSession[]; next_cursor?: string }>("/api/auth/sessions", {
    token,
    query: { cursor, limit: 20 },
  });
export const revokeSession = (token: string, id: string) =>
  request<{ sign_in_required: boolean }>(`/api/auth/sessions/${encodeURIComponent(id)}`, {
    token,
    method: "DELETE",
  });
export const revokeAllSessions = (token: string) =>
  request<{ sign_in_required: boolean }>("/api/auth/session-revocations", {
    token,
    method: "POST",
    json: { scope: "all" },
  });
