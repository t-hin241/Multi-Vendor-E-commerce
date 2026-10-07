import { ShipmentOperations } from "@/components/admin/shipment-operations";

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const id = params.shipment_id;
  return <ShipmentOperations focusID={typeof id === "string" ? id : undefined} />;
}
