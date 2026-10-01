"use client";

import Link from "next/link";

import { ApiError, isOutcomeUnknown } from "@/lib/api-client";

// ActionError shows why an admin action failed. When the outcome is
// unknown (no response, or a server error) it says so and points to the
// audit by request id, instead of inviting a blind resend.
export function ActionError({ error }: { error: unknown }) {
  if (!error) return null;
  const requestId = error instanceof ApiError ? error.requestId : undefined;
  if (isOutcomeUnknown(error)) {
    return (
      <div className="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
        <p className="font-medium">Result unknown</p>
        <p className="text-muted-foreground">
          The server did not confirm this action. It may or may not have been applied. Reload the
          page to see the current state
          {requestId ? (
            <>
              {" "}
              or look it up in the{" "}
              <Link
                className="underline"
                href={`/admin/audit?request_id=${encodeURIComponent(requestId)}`}
              >
                audit (request {requestId.slice(0, 8)})
              </Link>
            </>
          ) : null}{" "}
          before trying again.
        </p>
      </div>
    );
  }
  const message = error instanceof Error ? error.message : "Something went wrong.";
  return (
    <p className="text-sm text-destructive">
      {message}
      {requestId && (
        <span className="text-muted-foreground"> (request {requestId.slice(0, 8)})</span>
      )}
    </p>
  );
}
