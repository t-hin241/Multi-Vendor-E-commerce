import type { Product } from "@/lib/api-client";
import { formatMoney } from "@/lib/format";

export function productPriceText(product: Product, selectedVariantId?: string): string {
  if (selectedVariantId) {
    const price = product.variants?.find((v) => v.id === selectedVariantId)?.price_amount;
    return price && price > 0 ? formatMoney(price, product.currency) : "Chưa có giá";
  }
  const min = product.min_price ?? product.price_amount;
  const max = product.max_price ?? min;
  return min === max
    ? formatMoney(min, product.currency)
    : `${formatMoney(min, product.currency)} – ${formatMoney(max, product.currency)}`;
}
