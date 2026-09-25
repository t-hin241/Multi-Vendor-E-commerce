"use client";

import { Menu } from "lucide-react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect } from "react";

import { SystemStatus } from "@/components/system-status";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { useAuth } from "@/lib/auth-context";
import { ADMIN_NAV_GROUPS, ADMIN_NAV_LINKS } from "@/lib/admin-nav";
import { cn } from "@/lib/utils";

function NavGroups({ pathname, onNavigate }: { pathname: string; onNavigate?: () => void }) {
  return (
    <nav className="flex flex-col gap-4">
      {ADMIN_NAV_GROUPS.map((group) => (
        <div key={group.label}>
          <p className="px-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">
            {group.label}
          </p>
          <div className="mt-1 flex flex-col gap-1">
            {group.items.map((link) => {
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
          </div>
        </div>
      ))}
    </nav>
  );
}

export function AdminConsoleShell({ children }: { children: React.ReactNode }) {
  const { user, isReady } = useAuth();
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (isReady && (!user || user.role !== "admin")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  if (!user || user.role !== "admin") return null;

  const routeTitle = ADMIN_NAV_LINKS.find((link) => link.href === pathname)?.label ?? "Admin";

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 px-4 py-6 sm:px-6 md:flex-row md:items-start">
      <aside className="hidden shrink-0 md:sticky md:top-20 md:block md:w-56">
        <NavGroups pathname={pathname} />
      </aside>

      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-3 border-b pb-4">
          <Sheet>
            <SheetTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="md:hidden"
                aria-label="Open admin navigation menu"
              >
                <Menu className="size-5" />
              </Button>
            </SheetTrigger>
            <SheetContent side="left" className="w-64">
              <SheetHeader>
                <SheetTitle>Admin console</SheetTitle>
              </SheetHeader>
              <div className="px-4">
                <NavGroups pathname={pathname} />
              </div>
            </SheetContent>
          </Sheet>

          <h1 className="text-xl font-semibold tracking-tight">{routeTitle}</h1>

          <div className="ml-auto flex items-center gap-2">
            <SystemStatus />
            <span className="text-sm text-muted-foreground">{user.full_name}</span>
            <Badge variant="outline">Admin</Badge>
          </div>
        </div>

        <div className="mt-6">{children}</div>
      </div>
    </div>
  );
}
