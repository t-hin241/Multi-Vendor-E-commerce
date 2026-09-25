"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";

import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import type { AddressInput } from "@/lib/api-client";
import { type AddressFormInput, addressSchema } from "@/lib/schemas/address";

const FIELDS: { name: keyof AddressFormInput; label: string; span?: boolean }[] = [
  { name: "recipient_name", label: "Tên người nhận" },
  { name: "phone", label: "Số điện thoại" },
  { name: "province", label: "Tỉnh/Thành phố" },
  { name: "district", label: "Quận/Huyện" },
  { name: "ward", label: "Phường/Xã" },
  { name: "street_address", label: "Địa chỉ cụ thể", span: true },
];

export function AddressForm({
  defaultValues,
  onSubmit,
  onCancel,
  isSubmitting,
  submitLabel,
}: {
  defaultValues?: AddressInput;
  onSubmit: (values: AddressFormInput) => void | Promise<void>;
  onCancel?: () => void;
  isSubmitting: boolean;
  submitLabel: string;
}) {
  const form = useForm<AddressFormInput>({
    resolver: zodResolver(addressSchema),
    defaultValues: defaultValues ?? {
      recipient_name: "",
      phone: "",
      province: "",
      district: "",
      ward: "",
      street_address: "",
    },
  });

  return (
    <Form {...form}>
      <form
        onSubmit={form.handleSubmit(async (values) => {
          await onSubmit(values);
          if (!defaultValues) form.reset();
        })}
        className="grid gap-3 sm:grid-cols-2"
      >
        {FIELDS.map(({ name, label, span }) => (
          <FormField
            key={name}
            control={form.control}
            name={name}
            render={({ field }) => (
              <FormItem className={span ? "sm:col-span-2" : undefined}>
                <FormLabel>{label}</FormLabel>
                <FormControl>
                  <Input {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}
        <div className="flex gap-2 sm:col-span-2">
          <Button type="submit" disabled={isSubmitting}>
            {isSubmitting ? "Đang lưu…" : submitLabel}
          </Button>
          {onCancel && (
            <Button type="button" variant="outline" onClick={onCancel}>
              Hủy
            </Button>
          )}
        </div>
      </form>
    </Form>
  );
}
