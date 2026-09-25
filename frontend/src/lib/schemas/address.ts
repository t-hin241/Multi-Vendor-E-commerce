import { z } from "zod";

// 1:1 with api.AddressInput.
export const addressSchema = z.object({
  recipient_name: z.string().trim().min(1, "Recipient name is required."),
  phone: z
    .string()
    .trim()
    .regex(/^[0-9+()\s-]{8,15}$/, "Enter a valid phone number."),
  province: z.string().trim().min(1, "Province is required."),
  district: z.string().trim().min(1, "District is required."),
  ward: z.string().trim().min(1, "Ward is required."),
  street_address: z.string().trim().min(1, "Street address is required."),
});
export type AddressFormInput = z.infer<typeof addressSchema>;
