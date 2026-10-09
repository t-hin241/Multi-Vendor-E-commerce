import { DeliveryExceptionQueue } from "@/components/admin/delivery-exception-queue";

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const id = params.exception_id;
  return <DeliveryExceptionQueue focusID={typeof id === "string" ? id : undefined} />;
}
