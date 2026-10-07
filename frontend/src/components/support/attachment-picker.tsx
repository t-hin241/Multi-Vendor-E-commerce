"use client";

import { ImagePlus, X } from "lucide-react";
import { useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import type { SupportAttachment, SupportScope } from "@/lib/api-client";
import { useUploadSupportAttachment } from "@/lib/hooks/use-support-cases";
import { checkImageFile, supportErrorMessage } from "@/lib/support-cases";

export type PickedAttachment = SupportAttachment & { name: string };

// AttachmentPicker uploads each image as soon as it is chosen (to the
// caller's private uploads) and hands back the ids for the message. An
// upload never sent with a message is deleted by Order after 24 hours.
export function AttachmentPicker({
  scope,
  value,
  onChange,
  max,
  maxBytes,
  disabled,
  label = "Thêm ảnh",
}: {
  scope: SupportScope;
  value: PickedAttachment[];
  onChange: (next: PickedAttachment[]) => void;
  max: number;
  maxBytes: number;
  disabled?: boolean;
  label?: string;
}) {
  const input = useRef<HTMLInputElement>(null);
  const upload = useUploadSupportAttachment(scope);
  const [error, setError] = useState<string | null>(null);

  async function pick(files: FileList | null) {
    setError(null);
    if (!files) return;
    let next = value;
    for (const file of Array.from(files)) {
      if (next.length >= max) {
        setError(`Tối đa ${max} ảnh.`);
        break;
      }
      const problem = checkImageFile(file, maxBytes);
      if (problem) {
        setError(`${file.name}: ${problem}`);
        continue;
      }
      try {
        const uploaded = await upload.mutateAsync(file);
        next = [...next, { ...uploaded, name: file.name }];
        onChange(next);
      } catch (err) {
        setError(`${file.name}: ${supportErrorMessage(err, "Không tải ảnh lên được.")}`);
      }
    }
    if (input.current) input.current.value = "";
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {value.map((a) => (
          <span key={a.id} className="flex items-center gap-1 rounded-md border px-2 py-1 text-xs">
            {a.name}
            <button
              type="button"
              aria-label={`Bỏ ${a.name}`}
              className="text-muted-foreground hover:text-foreground"
              onClick={() => onChange(value.filter((v) => v.id !== a.id))}
            >
              <X className="size-3" />
            </button>
          </span>
        ))}
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled || upload.isPending || value.length >= max}
          onClick={() => input.current?.click()}
        >
          <ImagePlus className="size-4" />
          {upload.isPending ? "Đang tải…" : label}
        </Button>
        <input
          ref={input}
          type="file"
          accept="image/jpeg,image/png"
          multiple
          className="hidden"
          onChange={(e) => pick(e.target.files)}
        />
      </div>
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  );
}
