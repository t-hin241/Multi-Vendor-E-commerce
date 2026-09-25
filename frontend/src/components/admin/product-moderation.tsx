"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Fragment, useState } from "react";

import { AuditLogPanel } from "@/components/admin/audit-log-panel";
import { ConfirmDialog, ReasonDialog } from "@/components/admin/confirm-dialogs";
import { ProductStatusBadge } from "@/components/admin/status-badges";
import { StatusFilter } from "@/components/admin/status-filter";
import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";

const PRODUCT_STATUS_OPTIONS = ["pending_review", "approved", "rejected", ""] as const;

export function ProductModeration() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("pending_review");
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const productsQuery = useQuery({
    queryKey: ["admin-products", status],
    queryFn: () =>
      callWithAuth((token) => api.listProductsForModeration(token, { status: status || undefined })),
  });

  async function handleApprove(id: string) {
    await callWithAuth((token) => api.approveProduct(token, id));
    await queryClient.invalidateQueries({ queryKey: ["admin-products"] });
  }

  async function handleReject(id: string, reason: string) {
    await callWithAuth((token) => api.rejectProduct(token, id, reason));
    await queryClient.invalidateQueries({ queryKey: ["admin-products"] });
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader
        title="Product moderation"
        action={
          <StatusFilter status={status} onChange={setStatus} options={PRODUCT_STATUS_OPTIONS} />
        }
      />

      {productsQuery.error && (
        <p className="text-sm text-destructive">
          Could not load products:{" "}
          {productsQuery.error instanceof api.ApiError ? productsQuery.error.message : "unknown error"}
        </p>
      )}

      <Card>
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead>Price</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {productsQuery.data?.map((p) => (
                <Fragment key={p.id}>
                  <TableRow>
                    <TableCell className="font-medium whitespace-normal">{p.name}</TableCell>
                    <TableCell>{formatMoney(p.price_amount, p.currency)}</TableCell>
                    <TableCell>
                      <ProductStatusBadge status={p.status} />
                      {p.status === "rejected" && p.rejection_reason && (
                        <p className="mt-1 text-xs text-muted-foreground">{p.rejection_reason}</p>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-2">
                        <Button
                          type="button"
                          variant="link"
                          size="sm"
                          className="h-auto p-0"
                          onClick={() => setExpandedId(expandedId === p.id ? null : p.id)}
                        >
                          {expandedId === p.id ? "Hide details" : "View details"}
                        </Button>
                        {p.status === "pending_review" && (
                          <>
                            <ConfirmDialog
                              trigger={
                                <Button size="sm" variant="secondary">
                                  Approve
                                </Button>
                              }
                              title={`Approve "${p.name}"?`}
                              description="This product becomes visible on the storefront immediately."
                              confirmLabel="Approve"
                              onConfirm={() => handleApprove(p.id)}
                            />
                            <ReasonDialog
                              trigger={
                                <Button size="sm" variant="outline" className="text-destructive">
                                  Reject
                                </Button>
                              }
                              title={`Reject "${p.name}"?`}
                              description="The vendor will see this reason on the product."
                              confirmLabel="Reject"
                              onConfirm={(reason) => handleReject(p.id, reason)}
                            />
                          </>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                  {expandedId === p.id && (
                    <TableRow>
                      <TableCell colSpan={4} className="whitespace-normal bg-muted/30">
                        <ProductModerationDetail productId={p.id} />
                      </TableCell>
                    </TableRow>
                  )}
                </Fragment>
              ))}
            </TableBody>
          </Table>
          {productsQuery.data?.length === 0 && (
            <p className="p-6 text-center text-sm text-muted-foreground">
              No products for this filter.
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// ProductModerationDetail shows the full submission behind a product row —
// images, media, variants and stock — so the approve/reject decision above
// is informed by exactly what the vendor was required to supply before
// submitting. Fetched on demand (only for the expanded row) rather than for
// every row in the list.
function ProductModerationDetail({ productId }: { productId: string }) {
  const { callWithAuth } = useAuth();
  const detailQuery = useQuery({
    queryKey: ["admin-product-detail", productId],
    queryFn: () => callWithAuth((token) => api.getProductForModeration(token, productId)),
  });

  if (detailQuery.isPending) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  const p = detailQuery.data;
  if (!p) return null;

  return (
    <div className="flex flex-col gap-2 py-1">
      <p className="text-sm">{p.description || <span className="text-muted-foreground">No description</span>}</p>

      {p.images && p.images.length > 0 ? (
        <div className="flex flex-wrap gap-2">
          {p.images.map((img) => (
            // eslint-disable-next-line @next/next/no-img-element
            <img key={img.id} src={img.url} alt="" className="h-16 w-16 rounded object-cover" />
          ))}
        </div>
      ) : (
        <p className="text-xs text-warning-foreground">No image uploaded.</p>
      )}

      {p.media && p.media.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {p.media.map((m) =>
            m.kind === "video" ? (
              <video key={m.id} controls src={m.url} className="h-16 w-28 rounded bg-muted" />
            ) : (
              // eslint-disable-next-line @next/next/no-img-element
              <img key={m.id} src={m.url} alt="" className="h-16 w-16 rounded object-cover" />
            ),
          )}
        </div>
      )}

      {p.variants && p.variants.length > 0 ? (
        <ul className="flex flex-col gap-1 text-sm">
          {p.variants.map((v) => (
            <li key={v.id}>
              {v.sku} — {v.options.map((o) => `${o.attribute_name}: ${o.option_value}`).join(", ")} —
              stock: {v.available_quantity ?? 0}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm">
          Stock:{" "}
          {p.stock_quantity !== undefined ? (
            p.stock_quantity
          ) : (
            <span className="text-warning-foreground">not set up</span>
          )}
        </p>
      )}

      <AuditLogPanel
        targetId={productId}
        fetchLog={(token) => api.getProductAuditLog(token, productId)}
      />
    </div>
  );
}
