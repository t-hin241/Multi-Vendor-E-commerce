"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { useAuth } from "@/lib/auth-context";

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { user, isReady } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isReady && (!user || user.role !== "admin")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  if (!user || user.role !== "admin") return null;

  return (
    <div>
      <header className="border-b border-slate-200 bg-slate-900 px-6 py-4">
        <span className="text-sm font-medium text-slate-300">Admin Console</span>
      </header>
      {children}
    </div>
  );
}
