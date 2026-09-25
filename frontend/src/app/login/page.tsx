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
import { describeApiError } from "@/lib/errors";
import { useLogin } from "@/lib/hooks/use-auth-mutations";
import { type LoginInput, loginSchema } from "@/lib/schemas/auth";

export default function LoginPage() {
  const router = useRouter();
  const loginMutation = useLogin();
  const form = useForm<LoginInput>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: "", password: "" },
  });

  async function onSubmit(values: LoginInput) {
    try {
      await loginMutation.mutateAsync(values);
      router.push("/");
    } catch (err) {
      form.setError("root", {
        message: describeApiError(err, "Đăng nhập thất bại. Vui lòng thử lại."),
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
          <CardTitle className="text-xl">Đăng nhập</CardTitle>
          <CardDescription>Đăng nhập để tiếp tục mua sắm</CardDescription>
        </CardHeader>
        <CardContent>
          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="flex flex-col gap-4">
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
                      <Input type="password" autoComplete="current-password" {...field} />
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
                {form.formState.isSubmitting ? "Đang đăng nhập…" : "Đăng nhập"}
              </Button>
            </form>
          </Form>

          <p className="mt-4 text-sm text-muted-foreground">
            Chưa có tài khoản?{" "}
            <Link href="/register" className="text-primary underline">
              Đăng ký
            </Link>
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
