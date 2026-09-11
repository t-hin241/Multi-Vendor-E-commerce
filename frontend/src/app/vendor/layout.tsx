"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { useAuth } from "@/lib/auth-context";

export default function VendorLayout({ children }: { children: React.ReactNode }) {
  const { user, isReady } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isReady && (!user || user.role !== "vendor")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  if (!user || user.role !== "vendor") return null;

  return (
    <div>
      <header className="flex items-center gap-4 border-b border-slate-200 bg-white px-6 py-4">
        <span className="text-sm font-medium text-slate-500">Vendor Portal</span>
        <Link href="/vendor" className="text-sm text-slate-600 hover:text-slate-900">
          Dashboard
        </Link>
        <Link href="/vendor/orders" className="text-sm text-slate-600 hover:text-slate-900">
          Orders
        </Link>
      </header>
      {children}
    </div>
  );
}
