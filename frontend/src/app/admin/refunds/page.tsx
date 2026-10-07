import { RefundOperations } from "@/components/admin/refund-operations";

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const id = params.refund_id;
  return <RefundOperations focusID={typeof id === "string" ? id : undefined} />;
}
