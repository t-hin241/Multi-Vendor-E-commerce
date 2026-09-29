"use client";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ConfirmDialog, ReasonDialog } from "@/components/admin/confirm-dialogs";
import { PayoutAccounts } from "@/components/vendor/payout-accounts";
import { Button } from "@/components/ui/button";
import type { Vendor } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import * as ops from "@/lib/vendor-operations";

export function VendorOperations({ vendor }: { vendor: Vendor }) {
  const { callWithAuth } = useAuth();
  const cache = useQueryClient();
  const [payouts, setPayouts] = useState(false);
  async function change(action: "suspend" | "restore", reason: string) {
    await callWithAuth((t) => ops.changeStatus(t, vendor.id, action, reason));
    await cache.invalidateQueries({ queryKey: ["admin-vendor-applications"] });
  }
  return (
    <div className="mt-2 space-y-2 text-left whitespace-normal">
      {vendor.suspension_reason && <p className="text-sm">{vendor.suspension_reason}</p>}
      {vendor.enforcement_pending && (
        <p className="text-xs text-amber-700">
          Status propagation pending. Suspension is effective after Catalog and Order acknowledge
          it.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        {vendor.status === "approved" && (
          <ReasonDialog
            trigger={
              <Button size="sm" variant="destructive">
                Suspend
              </Button>
            }
            title={`Suspend ${vendor.shop_name}?`}
            description="Stops new sales. Existing orders remain available for fulfillment."
            confirmLabel="Suspend"
            onConfirm={(reason) => change("suspend", reason)}
          />
        )}
        {vendor.status === "suspended" && (
          <ReasonDialog
            trigger={
              <Button size="sm" variant="outline">
                Restore
              </Button>
            }
            title={`Restore ${vendor.shop_name}?`}
            description="Review the shop profile and pickup address before restoring selling permission."
            confirmLabel="Restore"
            onConfirm={(reason) => change("restore", reason)}
          />
        )}
        {vendor.enforcement_pending && (
          <ConfirmDialog
            trigger={
              <Button size="sm" variant="outline">
                Retry sync
              </Button>
            }
            title="Retry status propagation?"
            description="Requeues undelivered status events."
            confirmLabel="Retry"
            onConfirm={async () => {
              await callWithAuth((t) => ops.replay(t, vendor.id));
              await cache.invalidateQueries({ queryKey: ["admin-vendor-applications"] });
            }}
          />
        )}
        <Button size="sm" variant="outline" onClick={() => setPayouts(!payouts)}>
          {payouts ? "Close payout accounts" : "Payout accounts"}
        </Button>
      </div>
      {payouts && <PayoutAccounts key={vendor.id} vendorId={vendor.id} admin />}
    </div>
  );
}
