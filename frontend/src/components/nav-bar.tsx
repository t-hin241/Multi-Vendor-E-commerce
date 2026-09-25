"use client";

import { Menu, LogOut, ShoppingCart, Package, MapPin, Shield, Search } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";

import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { useAuth } from "@/lib/auth-context";
import { useCart } from "@/lib/hooks/use-cart";
import { VENDOR_NAV_LINKS, type VendorNavLink } from "@/lib/vendor-nav";

type NavLink = VendorNavLink;

function linksForRole(role: "buyer" | "vendor" | "admin" | undefined): NavLink[] {
  if (role === "buyer") {
    return [
      { href: "/cart", label: "Giỏ hàng", icon: ShoppingCart },
      { href: "/orders", label: "Đơn hàng của tôi", icon: Package },
      { href: "/addresses", label: "Địa chỉ", icon: MapPin },
    ];
  }
  if (role === "vendor") {
    return VENDOR_NAV_LINKS;
  }
  if (role === "admin") {
    return [{ href: "/admin", label: "Admin console", icon: Shield }];
  }
  return [];
}

function initials(fullName: string) {
  const parts = fullName.trim().split(/\s+/);
  const first = parts[0]?.[0] ?? "";
  const last = parts.length > 1 ? (parts[parts.length - 1]?.[0] ?? "") : "";
  return (first + last).toUpperCase();
}

export function NavBar() {
  const { user, logout, isReady } = useAuth();
  const router = useRouter();
  const [mobileOpen, setMobileOpen] = useState(false);
  const [searchValue, setSearchValue] = useState("");
  const cartQuery = useCart(user?.role === "buyer");

  function handleLogout() {
    logout();
    setMobileOpen(false);
    router.push("/");
  }

  function handleSearchSubmit(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = searchValue.trim();
    router.push(trimmed ? `/?q=${encodeURIComponent(trimmed)}` : "/");
  }

  const roleLinks = linksForRole(user?.role);
  const cartCount = cartQuery.data?.items.reduce((sum, line) => sum + line.quantity, 0) ?? 0;

  return (
    <header className="sticky top-0 z-40 border-b bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/80">
      <div className="mx-auto flex h-16 max-w-6xl items-center gap-4 px-4 sm:px-6">
        <Link href="/" className="flex shrink-0 items-center gap-2 font-semibold text-primary">
          <ShoppingCart className="size-5" />
          <span>Shopee Multi Vendor</span>
        </Link>

        {/* Desktop search */}
        <form onSubmit={handleSearchSubmit} className="hidden max-w-md flex-1 md:block">
          <div className="relative">
            <Search className="absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              placeholder="Tìm sản phẩm…"
              value={searchValue}
              onChange={(e) => setSearchValue(e.target.value)}
              className="pl-9"
              aria-label="Tìm sản phẩm"
            />
          </div>
        </form>

        {/* Desktop actions */}
        <div className="ml-auto hidden items-center gap-1 md:flex">
          {user?.role === "buyer" && (
            <Button variant="ghost" size="icon" className="relative" asChild>
              <Link href="/cart" aria-label="Giỏ hàng">
                <ShoppingCart className="size-5" />
                {cartCount > 0 && (
                  <Badge className="absolute top-0 right-0 h-4 min-w-4 justify-center rounded-full px-1 text-[10px]">
                    {cartCount}
                  </Badge>
                )}
              </Link>
            </Button>
          )}
          {!isReady ? null : user ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" className="gap-2 px-2">
                  <Avatar className="size-7">
                    <AvatarFallback className="text-xs">{initials(user.full_name)}</AvatarFallback>
                  </Avatar>
                  <span className="text-sm">{user.full_name}</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel>
                  <p className="font-medium">{user.full_name}</p>
                  <p className="text-xs font-normal text-muted-foreground capitalize">
                    {user.role === "buyer" ? "Tài khoản người mua" : `${user.role} account`}
                  </p>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                {roleLinks.map((link) => (
                  <DropdownMenuItem key={link.href} asChild>
                    <Link href={link.href}>
                      <link.icon className="size-4" />
                      {link.label}
                    </Link>
                  </DropdownMenuItem>
                ))}
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onClick={handleLogout}>
                  <LogOut className="size-4" />
                  Đăng xuất
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <>
              <Button variant="ghost" asChild>
                <Link href="/login">Đăng nhập</Link>
              </Button>
              <Button asChild>
                <Link href="/register">Đăng ký</Link>
              </Button>
            </>
          )}
        </div>

        {/* Mobile nav */}
        <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
          <SheetTrigger asChild>
            <Button variant="ghost" size="icon" className="ml-auto md:hidden" aria-label="Mở menu">
              <Menu className="size-5" />
            </Button>
          </SheetTrigger>
          <SheetContent side="right" className="w-72">
            <SheetHeader>
              <SheetTitle>Menu</SheetTitle>
            </SheetHeader>
            <div className="flex flex-col gap-1 px-4">
              {!isReady ? null : user ? (
                <>
                  <div className="flex items-center gap-2 pb-2">
                    <Avatar className="size-8">
                      <AvatarFallback className="text-xs">
                        {initials(user.full_name)}
                      </AvatarFallback>
                    </Avatar>
                    <div>
                      <p className="text-sm font-medium">{user.full_name}</p>
                      <p className="text-xs text-muted-foreground capitalize">{user.role}</p>
                    </div>
                  </div>
                  <Separator className="mb-2" />
                  {roleLinks.map((link) => (
                    <Button
                      key={link.href}
                      variant="ghost"
                      className="justify-start gap-2"
                      asChild
                      onClick={() => setMobileOpen(false)}
                    >
                      <Link href={link.href}>
                        <link.icon className="size-4" />
                        {link.label}
                      </Link>
                    </Button>
                  ))}
                  <Separator className="my-2" />
                  <Button
                    variant="ghost"
                    className="justify-start gap-2 text-destructive"
                    onClick={handleLogout}
                  >
                    <LogOut className="size-4" />
                    Đăng xuất
                  </Button>
                </>
              ) : (
                <>
                  <Button variant="outline" asChild onClick={() => setMobileOpen(false)}>
                    <Link href="/login">Đăng nhập</Link>
                  </Button>
                  <Button asChild onClick={() => setMobileOpen(false)}>
                    <Link href="/register">Đăng ký</Link>
                  </Button>
                </>
              )}
            </div>
          </SheetContent>
        </Sheet>
      </div>
    </header>
  );
}
