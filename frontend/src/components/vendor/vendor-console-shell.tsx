"use client";

import { useQuery } from "@tanstack/react-query";
import { Menu, Plus } from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect } from "react";

import { VendorStatusBadge } from "@/components/vendor/status-badges";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { resolveActiveVendor } from "@/lib/vendor";
import { VENDOR_NAV_LINKS } from "@/lib/vendor-nav";
import { cn } from "@/lib/utils";

function NavList({ pathname, onNavigate }: { pathname: string; onNavigate?: () => void }) {
  return (
    <nav className="flex flex-col gap-1">
      {VENDOR_NAV_LINKS.map((link) => {
        const isActive = pathname === link.href;
        return (
          <Button
            key={link.href}
            variant="ghost"
            className={cn(
              "justify-start gap-2",
              isActive && "bg-primary/10 text-primary hover:bg-primary/10 hover:text-primary",
            )}
            asChild
            onClick={onNavigate}
          >
            <Link href={link.href}>
              <link.icon className="size-4" />
              {link.label}
            </Link>
          </Button>
        );
      })}
    </nav>
  );
}

export function VendorConsoleShell({ children }: { children: React.ReactNode }) {
  const { user, isReady, callWithAuth, selectedVendorId, setSelectedVendorId } = useAuth();
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (isReady && (!user || user.role !== "vendor")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const vendorsQuery = useQuery({
    queryKey: ["my-vendors"],
    queryFn: () => callWithAuth((token) => api.listMyVendors(token)),
    enabled: Boolean(user && user.role === "vendor"),
  });

  const vendors = vendorsQuery.data ?? [];
  const activeVendor = resolveActiveVendor(vendors, selectedVendorId);

  // Persist the resolved fallback shop once vendors load, not just derive it
  // for display -- some vendor pages (orders, shipping) key their own
  // queries off the raw selectedVendorId from auth state rather than
  // re-deriving the fallback themselves, so a vendor who never explicitly
  // used the switcher (e.g. anyone who owns exactly one shop, where the
  // switcher never renders a picker at all) would otherwise never get it
  // set and those pages would incorrectly show "no shop selected".
  useEffect(() => {
    if (!selectedVendorId && activeVendor) {
      setSelectedVendorId(activeVendor.id);
    }
  }, [selectedVendorId, activeVendor, setSelectedVendorId]);

  if (!user || user.role !== "vendor") return null;

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-6 sm:px-6 md:flex-row md:items-start">
      <aside className="hidden shrink-0 md:sticky md:top-20 md:block md:w-56">
        <NavList pathname={pathname} />
      </aside>

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-3 border-b pb-4">
          <Sheet>
            <SheetTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="md:hidden"
                aria-label="Mở menu điều hướng kênh người bán"
              >
                <Menu className="size-5" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-64">
              <SheetHeader>
                <SheetTitle>Kênh người bán</SheetTitle>
              </SheetHeader>
              <div className="px-4">
                <NavList pathname={pathname} />
              </div>
            </SheetContent>
          </Sheet>

          {vendors.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Bạn chưa có cửa hàng nào.{" "}
              <Link href="/vendor/shops" className="text-primary underline">
                Đăng ký bán hàng
              </Link>
            </p>
          ) : (
            <>
              {vendors.length > 1 ? (
                <Select value={activeVendor?.id} onValueChange={setSelectedVendorId}>
                  <SelectTrigger className="min-w-40">
                    <SelectValue placeholder="Chọn cửa hàng" />
                  </SelectTrigger>
                  <SelectContent>
                    {vendors.map((v) => (
                      <SelectItem key={v.id} value={v.id}>
                        {v.shop_name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <span className="text-sm font-medium">{activeVendor?.shop_name}</span>
              )}
              {activeVendor && <VendorStatusBadge status={activeVendor.status} />}

              {activeVendor?.status === "approved" && (
                <Button size="sm" className="ml-auto" asChild>
                  <Link href="/vendor/products">
                    <Plus className="size-4" />
                    Thêm sản phẩm
                  </Link>
                </Button>
              )}
            </>
          )}
        </div>

        <div className="mt-6">{children}</div>
      </div>
    </div>
  );
}
