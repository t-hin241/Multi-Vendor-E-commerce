import { SupportIntakes } from "@/components/admin/support-intakes";
import { SupportQueue } from "@/components/admin/support-queue";

export default function AdminSupportPage() {
  return (
    <div className="flex flex-col gap-4">
      <SupportIntakes />
      <SupportQueue />
    </div>
  );
}
