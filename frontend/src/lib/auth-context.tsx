"use client";

import { createContext, useCallback, useContext, useMemo, useSyncExternalStore } from "react";

import * as api from "@/lib/api-client";

const STORAGE_KEY = "shopee.auth";

type StoredSession = {
  user: api.User;
  accessToken: string;
  refreshToken: string;
};

type AuthContextValue = {
  user: api.User | null;
  accessToken: string | null;
  isReady: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (
    email: string,
    password: string,
    fullName: string,
    role: "buyer" | "vendor",
  ) => Promise<void>;
  logout: () => void;
  // Calls fn with the current access token; on a 401 it refreshes the
  // session once (access tokens are short-lived, 15 minutes) and retries,
  // so a session doesn't die mid-demo just from sitting idle.
  callWithAuth: <T>(fn: (token: string) => Promise<T>) => Promise<T>;
};

const AuthContext = createContext<AuthContextValue | null>(null);

// The session lives in localStorage, an external store React doesn't own.
// useSyncExternalStore reads it without a hydration mismatch: the server
// (and initial client render) sees the "not loaded yet" sentinel via
// getServerSnapshot, then the real client snapshot takes over once mounted.
const NOT_LOADED = Symbol("not-loaded");
let cachedSnapshot: StoredSession | null | typeof NOT_LOADED = NOT_LOADED;
const listeners = new Set<() => void>();

function readFromStorage(): StoredSession | null {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    return raw ? (JSON.parse(raw) as StoredSession) : null;
  } catch {
    return null;
  }
}

function getSnapshot(): StoredSession | null | typeof NOT_LOADED {
  if (cachedSnapshot === NOT_LOADED) {
    cachedSnapshot = readFromStorage();
  }
  return cachedSnapshot;
}

function getServerSnapshot(): typeof NOT_LOADED {
  return NOT_LOADED;
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function setStoredSession(next: StoredSession | null) {
  cachedSnapshot = next;
  try {
    if (next) {
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
    } else {
      window.localStorage.removeItem(STORAGE_KEY);
    }
  } catch {
    // Storage can be unavailable (private browsing, quota); the session
    // just won't survive a reload, which is an acceptable degradation.
  }
  listeners.forEach((listener) => listener());
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
  const isReady = snapshot !== NOT_LOADED;
  const session = isReady ? (snapshot as StoredSession | null) : null;

  const applyResult = useCallback((result: api.AuthResult) => {
    setStoredSession({
      user: result.user,
      accessToken: result.access_token,
      refreshToken: result.refresh_token,
    });
  }, []);

  const login = useCallback(
    async (email: string, password: string) => {
      applyResult(await api.login(email, password));
    },
    [applyResult],
  );

  const register = useCallback(
    async (email: string, password: string, fullName: string, role: "buyer" | "vendor") => {
      applyResult(await api.register(email, password, fullName, role));
    },
    [applyResult],
  );

  const logout = useCallback(() => {
    const refreshToken = session?.refreshToken;
    setStoredSession(null);
    if (refreshToken) {
      api.logout(refreshToken).catch(() => {
        // Best-effort server-side revocation; the client session is already cleared.
      });
    }
  }, [session]);

  const callWithAuth = useCallback(
    async <T,>(fn: (token: string) => Promise<T>): Promise<T> => {
      if (!session) throw new api.ApiError(401, "unauthorized", "You must be signed in.");
      try {
        return await fn(session.accessToken);
      } catch (err) {
        if (err instanceof api.ApiError && err.status === 401) {
          const result = await api.refreshSession(session.refreshToken);
          applyResult(result);
          return fn(result.access_token);
        }
        throw err;
      }
    },
    [session, applyResult],
  );

  const value = useMemo<AuthContextValue>(
    () => ({
      user: session?.user ?? null,
      accessToken: session?.accessToken ?? null,
      isReady,
      login,
      register,
      logout,
      callWithAuth,
    }),
    [session, isReady, login, register, logout, callWithAuth],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within an AuthProvider");
  return ctx;
}
