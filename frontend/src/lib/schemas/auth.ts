import { z } from "zod";

export const loginSchema = z.object({
  email: z.string().min(1, "Email is required.").email("Enter a valid email address."),
  password: z.string().min(1, "Password is required."),
});
export type LoginInput = z.infer<typeof loginSchema>;

export const registerSchema = z.object({
  fullName: z.string().trim().min(1, "Full name is required."),
  email: z.string().min(1, "Email is required.").email("Enter a valid email address."),
  password: z
    .string()
    .min(8, "Password must be at least 8 characters.")
    .regex(/[0-9]/, "Password must include at least one number."),
  role: z.enum(["buyer", "vendor"]),
});
export type RegisterInput = z.infer<typeof registerSchema>;
