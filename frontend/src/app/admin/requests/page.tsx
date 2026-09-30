import { RestockModeration } from "@/components/admin/restock-moderation";
import { InventoryOperations } from "@/components/admin/inventory-operations";

export default function AdminRequestsPage() {
  return (
    <div className="space-y-8">
      <RestockModeration />
      <InventoryOperations />
    </div>
  );
}
