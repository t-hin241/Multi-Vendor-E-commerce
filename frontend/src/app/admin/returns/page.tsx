import { ReturnModeration } from "@/components/admin/return-moderation";

export default async function Page({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = await searchParams;
  const id = params.return_id;
  return <ReturnModeration focusID={typeof id === "string" ? id : undefined} />;
}
