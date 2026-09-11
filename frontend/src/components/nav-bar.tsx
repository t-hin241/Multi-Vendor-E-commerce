"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";

import { useAuth } from "@/lib/auth-context";

export function NavBar() {
  const { user, logout, isReady } = useAuth();
  const router = useRouter();

  function handleLogout() {
    logout();
    router.push("/");
  }

  return (
    <header className="border-b border-slate-200 bg-white px-6 py-4">
      <div className="mx-auto flex max-w-4xl items-center justify-between">
        <Link href="/" className="font-semibold text-slate-900">
          Shopee Multi Vendor
        </Link>

        <nav className="flex items-center gap-4 text-sm text-slate-600">
          <Link href="/" className="hover:text-slate-900">
            Storefront
          </Link>

          {!isReady ? null : user ? (
            <>
              {user.role === "buyer" && (
                <>
                  <Link href="/cart" className="hover:text-slate-900">
                    Cart
                  </Link>
                  <Link href="/orders" className="hover:text-slate-900">
                    My orders
                  </Link>
                  <Link href="/addresses" className="hover:text-slate-900">
                    Addresses
                  </Link>
                </>
              )}
              {user.role === "vendor" && (
                <>
                  <Link href="/vendor" className="hover:text-slate-900">
                    Vendor dashboard
                  </Link>
                  <Link href="/vendor/shipping" className="hover:text-slate-900">
                    Shipping
                  </Link>
                </>
              )}
              {user.role === "admin" && (
                <Link href="/admin" className="hover:text-slate-900">
                  Admin console
                </Link>
              )}
              <span className="text-slate-400">
                {user.full_name} ({user.role})
              </span>
              <button onClick={handleLogout} className="text-red-600 hover:underline">
                Log out
              </button>
            </>
          ) : (
            <>
              <Link href="/login" className="hover:text-slate-900">
                Log in
              </Link>
              <Link href="/register" className="hover:text-slate-900">
                Register
              </Link>
            </>
          )}
        </nav>
      </div>
    </header>
  );
}
