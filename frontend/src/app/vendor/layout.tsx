import { VendorConsoleShell } from "@/components/vendor/vendor-console-shell";

export default function VendorLayout({ children }: { children: React.ReactNode }) {
  return <VendorConsoleShell>{children}</VendorConsoleShell>;
}
