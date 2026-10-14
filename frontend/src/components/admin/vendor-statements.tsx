"use client";
import { useState } from "react";
import { StatementPanel } from "@/components/vendor/statement-panel";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
export function AdminVendorStatements() {
  const [input, setInput] = useState("");
  const [vendor, setVendor] = useState("");
  return (
    <section className="space-y-4">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setVendor(input.trim());
        }}
        className="flex flex-wrap items-end gap-2"
      >
        <label>
          Mã shop để tra sao kê
          <Input required value={input} onChange={(e) => setInput(e.target.value)} />
        </label>
        <Button type="submit">Tra sao kê</Button>
      </form>
      {vendor && <StatementPanel key={vendor} vendorId={vendor} admin />}
    </section>
  );
}
