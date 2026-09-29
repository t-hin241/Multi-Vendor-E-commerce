"use client";
import { PayoutAccounts } from "@/components/vendor/payout-accounts";
import { useAuth } from "@/lib/auth-context";
export default function PayoutAccountsPage() {
  const { selectedVendorId } = useAuth();
  return selectedVendorId ? (
    <PayoutAccounts key={selectedVendorId} vendorId={selectedVendorId} />
  ) : (
    <p>Chọn cửa hàng để quản lý tài khoản nhận tiền.</p>
  );
}
