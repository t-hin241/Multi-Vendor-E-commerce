"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { ShoppingCart } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { describeApiError } from "@/lib/errors";
import { useRegister } from "@/lib/hooks/use-auth-mutations";
import { type RegisterInput, registerSchema } from "@/lib/schemas/auth";

export default function RegisterPage() {
  const router = useRouter();
  const registerMutation = useRegister();
  const form = useForm<RegisterInput>({
    resolver: zodResolver(registerSchema),
    defaultValues: { fullName: "", email: "", password: "", role: "buyer" },
  });

  async function onSubmit(values: RegisterInput) {
    try {
      await registerMutation.mutateAsync(values);
      router.push(values.role === "vendor" ? "/vendor" : "/");
    } catch (err) {
      form.setError("root", {
        message: describeApiError(err, "Đăng ký thất bại. Vui lòng thử lại."),
      });
    }
  }

  return (
    <div className="mx-auto flex max-w-sm flex-col justify-center px-4 py-16 sm:px-6">
      <Link
        href="/"
        className="mb-6 flex items-center justify-center gap-2 text-lg font-semibold text-primary"
      >
        <ShoppingCart className="size-6" />
        Shopee Multi Vendor
      </Link>
      <Card>
        <CardHeader>
          <CardTitle className="text-xl">Tạo tài khoản</CardTitle>
          <CardDescription>Tạo tài khoản để mua sắm hoặc bắt đầu bán hàng</CardDescription>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="flex flex-col gap-4">
              <FormField
                control={form.control}
                name="fullName"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Họ và tên</FormLabel>
                    <FormControl>
                      <Input autoComplete="name" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="email"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Email</FormLabel>
                    <FormControl>
                      <Input type="email" autoComplete="email" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="password"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Mật khẩu</FormLabel>
                    <FormControl>
                      <Input type="password" autoComplete="new-password" {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name="role"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Bạn muốn</FormLabel>
                    <FormControl>
                      <RadioGroup
                        value={field.value}
                        onValueChange={field.onChange}
                        className="gap-2"
                      >
                        <div className="flex items-center gap-2">
                          <RadioGroupItem value="buyer" id="role-buyer" />
                          <Label htmlFor="role-buyer" className="font-normal">
                            Mua sắm với vai trò người mua
                          </Label>
                        </div>
                        <div className="flex items-center gap-2">
                          <RadioGroupItem value="vendor" id="role-vendor" />
                          <Label htmlFor="role-vendor" className="font-normal">
                            Bán hàng với vai trò người bán
                          </Label>
                        </div>
                      </RadioGroup>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {form.formState.errors.root && (
                <Alert variant="destructive">
                  <AlertDescription>{form.formState.errors.root.message}</AlertDescription>
                </Alert>
              )}

              <Button type="submit" disabled={form.formState.isSubmitting} className="mt-2">
                {form.formState.isSubmitting ? "Đang tạo tài khoản…" : "Đăng ký"}
              </Button>
            </form>
          </Form>

          <p className="mt-4 text-sm text-muted-foreground">
            Đã có tài khoản?{" "}
            <Link href="/login" className="text-primary underline">
              Đăng nhập
            </Link>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
