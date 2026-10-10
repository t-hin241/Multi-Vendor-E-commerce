"use client";

import { useQuery } from "@tanstack/react-query";
import { FilePlus, Film, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { AttachmentImage } from "@/components/support/attachment-image";
import { Button } from "@/components/ui/button";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

// PW-038: Shipment keeps the evidence of a failure report (the carrier's
// loss confirmation: photos or a short video). Files are uploaded for one
// shipment and cited by the report; one never cited is removed after a day.

const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const MAX_VIDEO_BYTES = 50 * 1024 * 1024;
export const MAX_SHIPMENT_EVIDENCE = 5;

export type PickedEvidence = api.ShipmentEvidence & { name: string };

// checkEvidenceFile is the client-side check; Shipment checks the content.
export function checkEvidenceFile(file: File): string | null {
  if (file.type === "video/mp4") {
    return file.size > MAX_VIDEO_BYTES ? "Video tối đa 50 MiB." : null;
  }
  if (file.type === "image/jpeg" || file.type === "image/png") {
    return file.size > MAX_IMAGE_BYTES ? "Ảnh tối đa 5 MiB." : null;
  }
  return "Chỉ nhận ảnh JPEG/PNG hoặc video MP4.";
}

export function ShipmentEvidencePicker({
  scope,
  shipmentId,
  value,
  onChange,
  disabled,
}: {
  scope: "vendor" | "admin";
  shipmentId: string;
  value: PickedEvidence[];
  onChange: (next: PickedEvidence[]) => void;
  disabled?: boolean;
}) {
  const { callWithAuth } = useAuth();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function pick(files: FileList | null) {
    setError(null);
    if (!files) return;
    setBusy(true);
    let next = value;
    for (const file of Array.from(files)) {
      if (next.length >= MAX_SHIPMENT_EVIDENCE) {
        setError(`Tối đa ${MAX_SHIPMENT_EVIDENCE} tệp.`);
        break;
      }
      const problem = checkEvidenceFile(file);
      if (problem) {
        setError(`${file.name}: ${problem}`);
        continue;
      }
      try {
        const uploaded = await callWithAuth((token) =>
          api.uploadShipmentEvidence(token, scope, shipmentId, file),
        );
        next = [...next, { ...uploaded, name: file.name }];
        onChange(next);
      } catch (err) {
        setError(`${file.name}: ${err instanceof Error ? err.message : "Không tải tệp lên được."}`);
      }
    }
    setBusy(false);
    if (input.current) input.current.value = "";
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {value.map((e) => (
          <span key={e.id} className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs">
            {e.name}
            <button
              type="button"
              aria-label={`Bỏ ${e.name}`}
              className="text-muted-foreground hover:text-foreground"
              onClick={() => onChange(value.filter((v) => v.id !== e.id))}
            >
              <X className="size-3" />
            </button>
          </span>
        ))}
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled || busy || value.length >= MAX_SHIPMENT_EVIDENCE}
          onClick={() => input.current?.click()}
        >
          <FilePlus className="size-4" />
          {busy ? "Đang tải…" : "Thêm ảnh/video chứng cứ"}
        </Button>
        <input
          ref={input}
          type="file"
          accept="image/jpeg,image/png,video/mp4"
          multiple
          className="hidden"
          aria-label="Tệp chứng cứ"
          onChange={(e) => pick(e.target.files)}
        />
      </div>
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}

function EvidenceVideo({
  scope,
  shipmentId,
  evidence,
}: {
  scope: "vendor" | "admin";
  shipmentId: string;
  evidence: api.ShipmentEvidence;
}) {
  const { callWithAuth } = useAuth();
  const [load, setLoad] = useState(false);
  const blob = useQuery({
    queryKey: ["shipment-evidence-file", scope, shipmentId, evidence.id],
    queryFn: () =>
      callWithAuth((token) => api.fetchShipmentEvidence(token, scope, shipmentId, evidence.id)),
    enabled: load,
    staleTime: Infinity,
    gcTime: 5 * 60_000,
    retry: 1,
  });
  const url = useMemo(() => (blob.data ? URL.createObjectURL(blob.data) : null), [blob.data]);
  useEffect(
    () => () => {
      if (url) URL.revokeObjectURL(url);
    },
    [url],
  );
  if (url) {
    return (
      <video src={url} controls className="h-20 rounded-md border" aria-label="Video chứng cứ" />
    );
  }
  return (
    <Button type="button" size="sm" variant="outline" onClick={() => setLoad(true)}>
      <Film className="size-4" />
      {blob.isFetching ? "Đang tải…" : blob.error ? "Không tải được video" : "Xem video"}
    </Button>
  );
}

// ShipmentEvidenceList shows the evidence of a shipment's reports; nothing
// renders while there is none.
export function ShipmentEvidenceList({
  scope,
  shipmentId,
}: {
  scope: "vendor" | "admin";
  shipmentId: string;
}) {
  const { callWithAuth } = useAuth();
  const list = useQuery({
    queryKey: ["shipment-evidence", scope, shipmentId],
    queryFn: () => callWithAuth((token) => api.listShipmentEvidence(token, scope, shipmentId)),
    staleTime: 60_000,
    retry: false,
  });
  const attached = (list.data ?? []).filter((e) => e.state === "attached");
  if (!attached.length) return null;
  return (
    <div className="flex flex-wrap items-center gap-2">
      {attached.map((e) =>
        e.content_type === "video/mp4" ? (
          <EvidenceVideo key={e.id} scope={scope} shipmentId={shipmentId} evidence={e} />
        ) : (
          <AttachmentImage
            key={e.id}
            scope={scope}
            caseId={shipmentId}
            attachment={{ id: e.id, content_type: e.content_type, size_bytes: e.size_bytes }}
            load={(token) => api.fetchShipmentEvidence(token, scope, shipmentId, e.id)}
          />
        ),
      )}
    </div>
  );
}
