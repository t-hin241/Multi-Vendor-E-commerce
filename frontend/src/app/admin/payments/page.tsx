import { PaymentReconciliation } from "@/components/admin/payment-reconciliation";
import { AdminVendorStatements } from "@/components/admin/vendor-statements";
export default function AdminPaymentsPage() {
  return (
    <div className="space-y-8">
      <AdminVendorStatements />
      <PaymentReconciliation />
    </div>
  );
}
