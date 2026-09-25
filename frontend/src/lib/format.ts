export function formatMoney(amount: number, currency = "VND") {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}
