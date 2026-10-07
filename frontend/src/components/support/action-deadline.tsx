export function ActionDeadline({
  dueAt,
  waitingOn,
}: {
  dueAt?: string | null;
  waitingOn?: string;
}) {
  if (!dueAt) return null;
  const waiting =
    waitingOn === "buyer" ? "người mua" : waitingOn === "vendor" ? "người bán" : "sàn";
  return (
    <p className="mt-2 text-sm text-muted-foreground">
      Hạn phản hồi: {new Date(dueAt).toLocaleString("vi-VN", { timeZone: "Asia/Ho_Chi_Minh" })} ·
      Đang chờ: {waiting}
    </p>
  );
}
