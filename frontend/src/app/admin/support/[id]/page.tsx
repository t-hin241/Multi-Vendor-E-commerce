"use client";

import { useParams } from "next/navigation";

import { SupportCaseConsole } from "@/components/admin/support-case-console";

export default function AdminSupportCasePage() {
  const params = useParams<{ id: string }>();
  return <SupportCaseConsole caseId={params.id} />;
}
