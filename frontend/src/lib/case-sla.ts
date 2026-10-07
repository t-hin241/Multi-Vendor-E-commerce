import { request } from "./api-client";

export type SLAOwner = "order" | "payment" | "shipment";
export type WorkItem = {
  id: string;
  resource_type: string;
  resource_id: string;
  stage: string;
  due_at: string;
  overall_due_at: string;
  paused_at?: string;
  waiting_on: string;
  assignee_id: string | null;
  version: number;
  deadline_version: number;
  needs_attention: boolean;
  breached_at?: string;
  legacy: boolean;
  url: string;
};
export type WorkPage = { items: WorkItem[]; next_cursor?: string; generated_at: string };
export type WorkSources = {
  sources: { name: SLAOwner; status: "ok" | "unavailable"; page: WorkPage | null }[];
};
export const ownerPaths: Record<SLAOwner, string> = {
  order: "/api/orders/admin/work-items",
  payment: "/api/payments/admin/work-items",
  shipment: "/api/shipments/admin/work-items",
};
export function workItems(
  token: string,
  status: string,
  cursors: Partial<Record<SLAOwner, string>>,
) {
  return request<WorkSources>("/api/admin/work-items", {
    token,
    query: {
      status,
      order_cursor: cursors.order,
      payment_cursor: cursors.payment,
      shipment_cursor: cursors.shipment,
    },
  });
}
export function changeWorkItem(
  token: string,
  owner: SLAOwner,
  id: string,
  action: "assignments" | "extensions" | "activations",
  body: { expected_version: number; reason: string; assignee_id?: string; new_due_at?: string },
  key: string,
) {
  return request<WorkItem>(`${ownerPaths[owner]}/${encodeURIComponent(id)}/${action}`, {
    token,
    method: "POST",
    json: body,
    headers: { "Idempotency-Key": key },
  });
}
// Ignore arbitrary URLs in responses; links are derived from known owners.
export function workItemURL(item: WorkItem): string {
  const id = encodeURIComponent(item.resource_id);
  switch (item.resource_type) {
    case "support":
      return `/admin/support/${id}`;
    case "return":
      return `/admin/returns?return_id=${id}`;
    case "refund":
      return `/admin/refunds?refund_id=${id}`;
    case "interception":
      return `/admin/fulfillment?shipment_id=${id}`;
    default:
      return "/admin/work-items";
  }
}
export function actionDue(item: WorkItem): string {
  return item.paused_at ? item.overall_due_at : item.due_at;
}
