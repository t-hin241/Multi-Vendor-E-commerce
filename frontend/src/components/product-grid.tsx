import { ProductCard } from "@/components/product-card";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import type { Product } from "@/lib/api-client";

export function ProductGridSkeleton({ count = 8 }: { count?: number }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4 lg:grid-cols-4">
      {Array.from({ length: count }).map((_, i) => (
        <Card key={i} className="gap-0 overflow-hidden py-0">
          <Skeleton className="aspect-square w-full rounded-none" />
          <CardContent className="flex flex-col gap-2 p-3">
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="mt-2 h-8 w-full" />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

export function ProductGrid({ products }: { products: Product[] }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 sm:gap-4 lg:grid-cols-4">
      {products.map((product) => (
        <ProductCard key={product.id} product={product} />
      ))}
    </div>
  );
}
