import type { Metadata } from "next";

import { Footer } from "@/components/footer";
import { NavBar } from "@/components/nav-bar";
import { Toaster } from "@/components/ui/sonner";
import { AuthProvider } from "@/lib/auth-context";
import { QueryProvider } from "@/lib/query-provider";

import "./globals.css";
import { Geist } from "next/font/google";
import { cn } from "@/lib/utils";

const geist = Geist({ subsets: ["latin"], variable: "--font-sans" });

export const metadata: Metadata = {
  title: "Shopee Multi Vendor",
  description: "Multi vendor marketplace",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={cn("font-sans", geist.variable)}>
      <body className="min-h-screen">
        <QueryProvider>
          <AuthProvider>
            <div className="flex min-h-screen flex-col">
              <NavBar />
              <main className="flex-1">{children}</main>
              <Footer />
            </div>
            <Toaster richColors position="top-right" />
          </AuthProvider>
        </QueryProvider>
      </body>
    </html>
  );
}
