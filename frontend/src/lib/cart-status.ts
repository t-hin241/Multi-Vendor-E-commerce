import { ApiError } from "@/lib/api-client";
import type { Cart, CartLine, CartLineState } from "@/lib/api-client";
import { formatMoney } from "@/lib/format";

// Presentation of Cart's line states. The Cart service decides every state;
// this module only words them for the buyer and never recomputes prices,
// stock or sellability on the client.

export type LineNotice = {
  label: string;
  hint: string;
  tone: "destructive" | "warning" | "info";
};

const lineNotices: Record<Exclude<CartLineState, "available">, LineNotice> = {
  removed: {
    label: "Sản phẩm không còn tồn tại",
    hint: "Người bán đã gỡ sản phẩm này. Hãy xóa khỏi giỏ để tiếp tục đặt hàng.",
    tone: "destructive",
  },
  under_review: {
    label: "Đang được cập nhật",
    hint: "Người bán đang sửa sản phẩm và chờ duyệt lại. Bạn có thể giữ trong giỏ nhưng chưa thể mua lúc này.",
    tone: "warning",
  },
  not_for_sale: {
    label: "Tạm ngừng bán",
    hint: "Sản phẩm tạm ngừng bán hoặc shop đang tạm ngưng hoạt động. Hãy xóa khỏi giỏ hoặc quay lại sau.",
    tone: "destructive",
  },
  option_required: {
    label: "Cần chọn phân loại",
    hint: "Sản phẩm đã có phân loại mới. Hãy xóa dòng này và thêm lại với phân loại bạn muốn.",
    tone: "warning",
  },
  option_unavailable: {
    label: "Phân loại không còn",
    hint: "Phân loại bạn chọn không còn được bán. Hãy xóa dòng này và chọn phân loại khác.",
    tone: "destructive",
  },
  out_of_stock: {
    label: "Hết hàng",
    hint: "Sản phẩm hiện đã hết hàng. Hãy xóa khỏi giỏ hoặc quay lại sau.",
    tone: "destructive",
  },
  insufficient_stock: {
    label: "Không đủ hàng",
    hint: "Số lượng trong giỏ vượt quá số hàng còn lại. Hãy giảm số lượng.",
    tone: "warning",
  },
  unverified: {
    label: "Chưa kiểm tra được",
    hint: "Tạm thời không kiểm tra được giá và tình trạng sản phẩm. Hãy tải lại giỏ hàng sau ít phút.",
    tone: "info",
  },
};

export function lineNotice(line: CartLine): LineNotice | null {
  if (line.state === "available") return null;
  const notice = lineNotices[line.state];
  if (line.state === "insufficient_stock" && line.available_quantity !== undefined) {
    return { ...notice, hint: `Chỉ còn ${line.available_quantity} sản phẩm. Hãy giảm số lượng.` };
  }
  return notice;
}

// Describes a price that changed since the buyer last accepted it, or null.
export function priceChangeText(line: CartLine): string | null {
  if (!line.price_changed || line.seen_price_amount === undefined || line.price_amount === null) {
    return null;
  }
  const before = formatMoney(line.seen_price_amount, line.seen_currency ?? line.currency);
  const now = formatMoney(line.price_amount, line.currency);
  return line.price_amount > line.seen_price_amount
    ? `Giá đã tăng từ ${before} lên ${now}`
    : `Giá đã giảm từ ${before} xuống ${now}`;
}

// Lines whose new price the buyer can accept, with the price they are shown.
export function priceConfirmations(cart: Cart) {
  return cart.items
    .filter((line) => line.price_changed && line.price_amount !== null && line.currency)
    .map((line) => ({
      line_id: line.line_id,
      price_amount: line.price_amount as number,
      currency: line.currency as string,
    }));
}

// Why the buyer cannot place the order yet, most actionable first. Empty
// when Cart reports the cart ready for checkout.
export function checkoutBlockers(cart: Cart): string[] {
  if (cart.checkout_ready) return [];
  const reasons: string[] = [];
  if (cart.line_count === 0) reasons.push("Giỏ hàng đang trống.");
  if (cart.price_changed_lines > 0) {
    reasons.push("Một số sản phẩm đã đổi giá. Hãy xác nhận giá mới trước khi đặt hàng.");
  }
  if (cart.unavailable_lines > 0) {
    reasons.push(
      `${cart.unavailable_lines} sản phẩm chưa thể mua. Hãy xử lý theo hướng dẫn ở từng sản phẩm.`,
    );
  }
  if (cart.mixed_currency) {
    reasons.push("Giỏ hàng có sản phẩm ở nhiều loại tiền tệ. Hãy đặt riêng từng loại.");
  }
  if (cart.over_line_limit) {
    reasons.push(
      `Giỏ hàng chỉ đặt được tối đa ${cart.limits.max_lines} sản phẩm khác nhau mỗi lần.`,
    );
  }
  if (cart.degraded.catalog) {
    reasons.push("Chưa kiểm tra được giá hiện tại. Hãy tải lại giỏ hàng.");
  }
  if (reasons.length === 0) reasons.push("Giỏ hàng chưa sẵn sàng. Hãy tải lại giỏ hàng.");
  return reasons;
}

// The cart changed elsewhere (another tab, device, or a finished checkout)
// or a price moved: the buyer must review the refreshed cart.
export function isCartChanged(err: unknown): boolean {
  return err instanceof ApiError && err.code === "cart_changed";
}
