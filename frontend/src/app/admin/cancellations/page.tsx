import { CancellationQueue } from "@/components/admin/cancellation-queue";

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const id = params.request_id;
  return <CancellationQueue focusID={typeof id === "string" ? id : undefined} />;
}
