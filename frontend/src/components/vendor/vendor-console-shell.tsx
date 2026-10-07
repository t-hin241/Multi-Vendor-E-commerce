"use client";

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
import { Badge } from "@/components/ui/badge";
import { useAuth } from "@/lib/auth-context";
import { useConsoleShops, type ConsoleShop } from "@/lib/hooks/use-console-shops";
import { can, canOpen } from "@/lib/shop-access";
import { VENDOR_NAV_LINKS } from "@/lib/vendor-nav";
import { cn } from "@/lib/utils";

function NavList({
  pathname,
  shop,
  accountRole,
  onNavigate,
}: {
  pathname: string;
  shop: ConsoleShop | undefined;
  accountRole: string | undefined;
  onNavigate?: () => void;
}) {
  // AF-17: only the pages this person may use in the active shop. The
  // services still check every request.
  const links = VENDOR_NAV_LINKS.filter((link) => canOpen(link.href, shop, accountRole));
  return (
    <nav className="flex flex-col gap-1">
      {links.map((link) => {
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
  const { user, isReady, selectedVendorId, setSelectedVendorId } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  // AF-17: a shop's staff may hold a buyer account; which shops they may
  // open comes from their memberships, not from the account role.
  const consoleRole = user?.role === "vendor" || user?.role === "buyer";

  useEffect(() => {
    if (isReady && (!user || !consoleRole)) {
      router.replace("/login");
    }
  }, [isReady, user, consoleRole, router]);

  const { query: shopsQuery, shops: vendors, activeShop: activeVendor } = useConsoleShops();

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

  if (!user || !consoleRole) return null;

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-6 sm:px-6 md:flex-row md:items-start">
      <aside className="hidden shrink-0 md:sticky md:top-20 md:block md:w-56">
        <NavList pathname={pathname} shop={activeVendor} accountRole={user.role} />
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
                <NavList pathname={pathname} shop={activeVendor} accountRole={user.role} />
              </div>
            </SheetContent>
          </Sheet>

          {shopsQuery.isError ? (
            <p className="text-sm text-destructive">
              Chưa tải được danh sách cửa hàng. Vui lòng tải lại trang.
            </p>
          ) : vendors.length === 0 && shopsQuery.isPending ? (
            <p className="text-sm text-muted-foreground">Đang tải…</p>
          ) : vendors.length === 0 && user.role === "vendor" ? (
            <p className="text-sm text-muted-foreground">
              Bạn chưa có cửa hàng nào.{" "}
              <Link href="/vendor/shops" className="text-primary underline">
                Đăng ký bán hàng
              </Link>
            </p>
          ) : vendors.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Bạn chưa là nhân viên của cửa hàng nào. Mở liên kết trong email mời để tham gia.
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
              {activeVendor?.role === "staff" && <Badge variant="outline">Nhân viên</Badge>}

              {activeVendor?.status === "approved" && can(activeVendor, "products.write") && (
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
