"use client";

import Link from "next/link";

import { useCategories } from "@/lib/hooks/use-categories";

export function Footer() {
  const categoriesQuery = useCategories();
  const rootCategories = (categoriesQuery.data?.filter((c) => c.parent_id == null) ?? []).slice(
    0,
    6,
  );

  return (
    <footer className="border-t bg-muted/30">
      <div className="mx-auto grid max-w-6xl gap-8 px-4 py-8 text-sm text-muted-foreground sm:grid-cols-3 sm:px-6">
        <div>
          <p className="font-medium text-foreground">Shopee Multi Vendor</p>
          <p className="mt-1">Marketplace nhiều gian hàng, mua sắm dễ dàng.</p>
        </div>

        {rootCategories.length > 0 && (
          <div>
            <p className="font-medium text-foreground">Danh mục</p>
            <nav className="mt-2 flex flex-col gap-1.5">
              {rootCategories.map((c) => (
                <Link key={c.id} href={`/categories/${c.slug}`} className="hover:text-foreground">
                  {c.name}
                </Link>
              ))}
            </nav>
          </div>
        )}

        <div>
          <p className="font-medium text-foreground">Hỗ trợ</p>
          <nav className="mt-2 flex flex-col gap-1.5">
            <Link href="/" className="hover:text-foreground">
              Trang chủ
            </Link>
            <Link href="/orders" className="hover:text-foreground">
              Đơn hàng của tôi
            </Link>
            <Link href="/register" className="hover:text-foreground">
              Bán hàng cùng chúng tôi
            </Link>
          </nav>
        </div>
      </div>
      <div className="border-t px-4 py-4 text-center text-xs text-muted-foreground sm:px-6">
        &copy; {new Date().getFullYear()} Shopee Multi Vendor.
      </div>
    </footer>
  );
}
