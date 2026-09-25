"use client";

import { useMutation } from "@tanstack/react-query";

import { useAuth } from "@/lib/auth-context";

// Thin useMutation wrappers around AuthContext's login/register — gives the
// login/register pages `isPending`/`error` for free instead of hand-rolled
// useState, while AuthContext itself stays the one seam that owns session
// storage. Navigation after success is page-level policy (login always goes
// to "/", register branches on role), so it stays in the page's onSuccess,
// not here.
export function useLogin() {
  const { login } = useAuth();
  return useMutation({
    mutationFn: (vars: { email: string; password: string }) => login(vars.email, vars.password),
  });
}

export function useRegister() {
  const { register } = useAuth();
  return useMutation({
    mutationFn: (vars: {
      email: string;
      password: string;
      fullName: string;
      role: "buyer" | "vendor";
    }) => register(vars.email, vars.password, vars.fullName, vars.role),
  });
}
