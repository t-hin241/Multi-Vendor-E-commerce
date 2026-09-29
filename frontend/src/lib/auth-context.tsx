"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useSyncExternalStore,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import * as api from "@/lib/api-client";
import { refreshOnce, withSessionLock } from "@/lib/session-refresh";

type Session = { user: api.User; accessToken: string; selectedVendorId?: string };
type AuthContextValue = {
  user: api.User | null;
  accessToken: string | null;
  isReady: boolean;
  selectedVendorId: string | null;
  setSelectedVendorId: (id: string | null) => void;
  login: (email: string, password: string) => Promise<void>;
  register: (
    email: string,
    password: string,
    fullName: string,
    role: "buyer" | "vendor",
  ) => Promise<void>;
  logout: () => Promise<void>;
  callWithAuth: <T>(fn: (token: string) => Promise<T>) => Promise<T>;
};
const AuthContext = createContext<AuthContextValue | null>(null);
const NOT_LOADED = Symbol("not-loaded");
let current: Session | null | typeof NOT_LOADED = NOT_LOADED;
let generation = 0;
const listeners = new Set<() => void>();
function subscribe(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}
function publish(value: Session | null) {
  current = value;
  listeners.forEach((fn) => fn());
}
function broadcast() {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel("shopee-session");
  channel.postMessage("changed");
  channel.close();
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const queryClient = useQueryClient();
  const snapshot = useSyncExternalStore<Session | null | typeof NOT_LOADED>(
    subscribe,
    () => current,
    () => NOT_LOADED,
  );
  const session = snapshot === NOT_LOADED ? null : snapshot;
  const clearCache = useCallback(() => {
    void queryClient.cancelQueries();
    queryClient.clear();
  }, [queryClient]);
  const restore = useCallback(async () => {
    const expected = generation;
    try {
      const result = await refreshOnce();
      if (expected !== generation) return;
      const previous = current === NOT_LOADED ? null : current;
      if (previous?.user.id !== result.user.id) clearCache();
      publish({
        user: result.user,
        accessToken: result.access_token,
        selectedVendorId:
          previous?.user.id === result.user.id ? previous.selectedVendorId : undefined,
      });
    } catch (err) {
      if (expected !== generation) return;
      if (err instanceof api.ApiError && err.status === 401) {
        clearCache();
        publish(null);
      } else {
        if (current === NOT_LOADED) publish(null);
        toast.error("Không thể kiểm tra phiên đăng nhập. Vui lòng thử lại.");
      }
    }
  }, [clearCache]);
  useEffect(() => {
    try {
      localStorage.removeItem("shopee.auth");
    } catch {
      /* Storage may be disabled. */
    }
    void restore();
    const channel =
      typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel("shopee-session");
    if (channel)
      channel.onmessage = () => {
        generation++;
        clearCache();
        publish(null);
        void restore();
      };
    return () => channel?.close();
  }, [restore, clearCache]);
  const authenticate = useCallback(
    async (action: () => Promise<api.AuthResult>) => {
      await withSessionLock(async () => {
        const result = await action();
        generation++;
        clearCache();
        publish({ user: result.user, accessToken: result.access_token });
        broadcast();
      });
    },
    [clearCache],
  );
  const login = useCallback(
    (email: string, password: string) => authenticate(() => api.login(email, password)),
    [authenticate],
  );
  const register = useCallback(
    (email: string, password: string, fullName: string, role: "buyer" | "vendor") =>
      authenticate(() => api.register(email, password, fullName, role)),
    [authenticate],
  );
  const logout = useCallback(async () => {
    await withSessionLock(async () => {
      try {
        await api.logout();
      } catch (err) {
        if (!(err instanceof api.ApiError && err.status === 401)) throw err;
      }
      generation++;
      clearCache();
      publish(null);
      broadcast();
    });
  }, [clearCache]);
  const setSelectedVendorId = useCallback(
    (id: string | null) => {
      if (!current || current === NOT_LOADED) return;
      clearCache();
      publish({ ...current, selectedVendorId: id ?? undefined });
    },
    [clearCache],
  );
  const callWithAuth = useCallback(
    async <T,>(fn: (token: string) => Promise<T>): Promise<T> => {
      const before = current;
      if (!before || before === NOT_LOADED)
        throw new api.ApiError(401, "unauthorized", "You must be signed in.");
      try {
        return await fn(before.accessToken);
      } catch (err) {
        if (!(err instanceof api.ApiError && err.status === 401)) throw err;
        const expected = generation;
        let result: api.AuthResult;
        try {
          result = await refreshOnce();
        } catch (refreshError) {
          if (
            generation === expected &&
            refreshError instanceof api.ApiError &&
            refreshError.status === 401
          ) {
            clearCache();
            publish(null);
          }
          throw refreshError;
        }
        if (generation !== expected || result.user.id !== before.user.id)
          throw new api.ApiError(401, "session_changed", "Session changed. Please retry.");
        publish({ ...before, user: result.user, accessToken: result.access_token });
        return fn(result.access_token);
      }
    },
    [clearCache],
  );
  const value = useMemo<AuthContextValue>(
    () => ({
      user: session?.user ?? null,
      accessToken: session?.accessToken ?? null,
      isReady: snapshot !== NOT_LOADED,
      selectedVendorId: session?.selectedVendorId ?? null,
      setSelectedVendorId,
      login,
      register,
      logout,
      callWithAuth,
    }),
    [session, snapshot, setSelectedVendorId, login, register, logout, callWithAuth],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within an AuthProvider");
  return ctx;
}
