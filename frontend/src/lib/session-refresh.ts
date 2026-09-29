import { refreshSession, type AuthResult } from "./api-client";

// Serialize cookie rotation across tabs without sharing credentials in storage.
let active: Promise<AuthResult> | null = null;
let queue: Promise<unknown> = Promise.resolve();
export function withSessionLock<T>(action: () => Promise<T>): Promise<T> {
  if (typeof navigator !== "undefined" && navigator.locks)
    return navigator.locks.request("shopee-session", action);
  const next = queue.then(action, action);
  queue = next.catch(() => undefined);
  return next;
}
export function refreshOnce(): Promise<AuthResult> {
  if (!active)
    active = withSessionLock(refreshSession).finally(() => {
      active = null;
    });
  return active;
}
