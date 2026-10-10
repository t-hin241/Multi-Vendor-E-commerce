"use client";

import { useQuery } from "@tanstack/react-query";
import { ImageOff } from "lucide-react";
import { useEffect, useMemo } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// AttachmentImage shows a private evidence image. The bytes are read with
// the viewer's token and shown from a blob URL, which never leaves this tab
// and is released when the thumbnail goes away.
export function AttachmentImage({
  scope,
  caseId,
  attachment,
  load,
}: {
  scope: api.SupportScope;
  caseId: string;
  attachment: api.SupportAttachment;
  // load reads the bytes from another route (receipt evidence, PW-038).
  load?: (token: string) => Promise<Blob>;
}) {
  const { callWithAuth } = useAuth();
  const query = useQuery({
    queryKey: ["support-attachment", scope, caseId, attachment.id],
    queryFn: () =>
      callWithAuth((token) =>
        load ? load(token) : api.fetchSupportAttachment(token, scope, caseId, attachment.id),
      ),
    staleTime: Infinity,
    gcTime: 5 * 60_000,
    retry: 1,
  });
  const url = useMemo(() => (query.data ? URL.createObjectURL(query.data) : null), [query.data]);
  useEffect(
    () => () => {
      if (url) URL.revokeObjectURL(url);
    },
    [url],
  );

  if (query.error) {
    return (
      <span className="flex size-20 items-center justify-center rounded-md border text-muted-foreground">
        <ImageOff className="size-5" aria-label="Không tải được ảnh" />
      </span>
    );
  }
  if (!url) return <span className="size-20 animate-pulse rounded-md bg-muted" />;
  return (
    <a href={url} target="_blank" rel="noreferrer noopener" className="block">
      {/* eslint-disable-next-line @next/next/no-img-element -- a private blob, not a static asset */}
      <img src={url} alt="Ảnh đính kèm" className="size-20 rounded-md border object-cover" />
    </a>
  );
}
