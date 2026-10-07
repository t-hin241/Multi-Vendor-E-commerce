import type { VariantProps } from "class-variance-authority";

import { StatusBadge } from "@/components/status-badge";
import type { badgeVariants } from "@/components/ui/badge";
import type { SupportCaseStatus } from "@/lib/api-client";
import { adminSupportStatusLabel, supportStatusLabel } from "@/lib/support-cases";

type Variant = VariantProps<typeof badgeVariants>["variant"];

const VARIANT: Record<SupportCaseStatus, Variant> = {
  open: "warning",
  in_progress: "info",
  waiting_buyer: "outline",
  waiting_vendor: "warning",
  resolution_pending: "info",
  resolved: "success",
  closed: "secondary",
};

const STATUSES = Object.keys(VARIANT) as SupportCaseStatus[];
const VI = Object.fromEntries(STATUSES.map((s) => [s, supportStatusLabel(s)])) as Record<
  SupportCaseStatus,
  string
>;
const EN = Object.fromEntries(STATUSES.map((s) => [s, adminSupportStatusLabel(s)])) as Record<
  SupportCaseStatus,
  string
>;

export function SupportStatusBadge({
  status,
  locale = "vi",
}: {
  status: SupportCaseStatus;
  locale?: "vi" | "en";
}) {
  return <StatusBadge status={status} variantMap={VARIANT} labelMap={locale === "en" ? EN : VI} />;
}
