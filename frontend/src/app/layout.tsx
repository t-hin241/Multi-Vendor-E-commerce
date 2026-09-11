import type { Metadata } from "next";

import { NavBar } from "@/components/nav-bar";
import { AuthProvider } from "@/lib/auth-context";
import { QueryProvider } from "@/lib/query-provider";

import "./globals.css";

export const metadata: Metadata = {
  title: "Shopee Multi Vendor",
  description: "Multi vendor marketplace",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="min-h-screen">
        <QueryProvider>
          <AuthProvider>
            <NavBar />
            {children}
          </AuthProvider>
        </QueryProvider>
      </body>
    </html>
  );
}
