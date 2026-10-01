import { Suspense } from "react";

import { AuditSearch } from "@/components/admin/audit-search";

export default function AdminAuditPage() {
  return (
    <Suspense fallback={null}>
      <AuditSearch />
    </Suspense>
  );
}
