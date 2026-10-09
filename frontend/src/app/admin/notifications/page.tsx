import { NotificationDelivery } from "@/components/admin/notification-delivery";
import { VendorActionNotices } from "@/components/admin/vendor-action-notices";

export default function AdminNotificationsPage() {
  return (
    <div className="flex flex-col gap-8">
      <NotificationDelivery />
      <VendorActionNotices />
    </div>
  );
}
