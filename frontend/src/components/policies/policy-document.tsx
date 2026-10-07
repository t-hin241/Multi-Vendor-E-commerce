import Link from "next/link";

import type { MarketplacePolicy } from "@/lib/api-client";
import { formatEffectiveAt, policyKindLabel } from "@/lib/policies";

// PolicyDocument shows one published version. The text is plain text
// (Vendor refuses HTML); React escapes it and line breaks are kept.
export function PolicyDocument({
  policy,
  current,
}: {
  policy: MarketplacePolicy;
  current: boolean;
}) {
  return (
    <article className="flex flex-col gap-4">
      <header>
        <p className="text-sm text-muted-foreground">{policyKindLabel(policy.kind)}</p>
        <h1 className="text-2xl font-semibold tracking-tight">{policy.title}</h1>
        <p className="mt-1 text-sm text-muted-foreground">
          Phiên bản {policy.version} · hiệu lực từ {formatEffectiveAt(policy.effective_at)}
          {!current && (
            <>
              {" "}
              ·{" "}
              <Link href={`/policies/${policy.kind}`} className="text-primary underline">
                xem bản đang áp dụng
              </Link>
            </>
          )}
        </p>
      </header>
      <p className="rounded-md border bg-muted/30 p-3 text-sm">{policy.summary}</p>
      <div className="whitespace-pre-wrap break-words text-sm leading-relaxed">
        {policy.content}
      </div>
      <section className="rounded-md border p-3 text-sm">
        <p className="font-medium">Liên hệ</p>
        <p className="whitespace-pre-wrap text-muted-foreground">{policy.contact}</p>
      </section>
      <p className="font-mono text-xs text-muted-foreground">
        Mã nội dung {policy.content_hash.slice(0, 12)}
      </p>
    </article>
  );
}
