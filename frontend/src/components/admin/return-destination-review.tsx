"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { Button } from "@/components/ui/button";
import * as api from "@/lib/api-client";
import { ApiError } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { carrierCheckNote } from "@/lib/return-destinations";

// ReturnDestinationReview lets an admin verify the address a shop chose
// for returned goods (AF-05). Approved returns only send buyers to a
// destination verified at its current version.
export function ReturnDestinationReview({ vendorId }: { vendorId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const destination = useQuery({
    queryKey: ["admin-return-destination", vendorId],
    queryFn: () => callWithAuth((token) => api.getAdminReturnDestination(token, vendorId)),
    enabled: open,
    retry: false,
  });

  if (!open) {
    return (
      <Button size="sm" variant="ghost" className="mt-1" onClick={() => setOpen(true)}>
        Return address
      </Button>
    );
  }
  const d = destination.data;
  const missing = destination.error instanceof ApiError && destination.error.status === 404;

  async function decide(verify: boolean, reason: string) {
    if (!d) return;
    setError(null);
    try {
      await callWithAuth((token) =>
        api.decideReturnDestination(token, vendorId, { version: d.version, verify, reason }),
      );
    } catch (err) {
      setError(err);
    }
    await queryClient.invalidateQueries({ queryKey: ["admin-return-destination", vendorId] });
  }

  return (
    <div className="mt-1 flex flex-col items-end gap-1 text-left text-xs">
      {missing && <p className="text-muted-foreground">No return address chosen yet.</p>}
      {d && (
        <>
          <p className="max-w-72 whitespace-normal">
            {d.recipient_name} ({d.phone}), {d.street_address}, {d.ward}, {d.district}, {d.province}{" "}
            · {d.receiving_hours}
          </p>
          <p className={d.verified ? "text-muted-foreground" : "text-destructive"}>
            v{d.version} · {d.verified ? "verified" : "not verified"}
            {d.rejection_reason && ` · ${d.rejection_reason}`}
          </p>
          {carrierCheckNote(d) && <p className="text-muted-foreground">{carrierCheckNote(d)}</p>}
          {!d.verified && (
            <div className="flex gap-2">
              <ReasonDialog
                variant="default"
                trigger={<Button size="sm">Verify</Button>}
                title="Verify this return address?"
                description="Confirm with the shop that it receives returned parcels here, at these hours."
                confirmLabel="Verify"
                onConfirm={(reason) => decide(true, reason)}
              />
              <ReasonDialog
                trigger={
                  <Button size="sm" variant="outline">
                    Reject
                  </Button>
                }
                title="Reject this return address?"
                description="The shop sees the reason and chooses again."
                confirmLabel="Reject"
                onConfirm={(reason) => decide(false, reason)}
              />
            </div>
          )}
        </>
      )}
      <ActionError error={error ?? (missing ? null : destination.error)} />
    </div>
  );
}
