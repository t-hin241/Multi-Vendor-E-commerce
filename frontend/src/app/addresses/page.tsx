"use client";

import { Pencil, Star, Trash2 } from "lucide-react";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import { AddressForm } from "@/components/address-form";
import { PageShell } from "@/components/page-shell";
import { SectionHeader } from "@/components/section-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/states/query-state";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { AddressInput, BuyerAddress } from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  useAddAddress,
  useAddresses,
  useDeleteAddress,
  useSetDefaultAddress,
  useUpdateAddress,
} from "@/lib/hooks/use-addresses";
import { cn } from "@/lib/utils";

export default function AddressesPage() {
  const { user, isReady } = useAuth();
  const router = useRouter();
  const [editing, setEditing] = useState<BuyerAddress | null>(null);
  const [formOpen, setFormOpen] = useState(false);

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const enabled = Boolean(user && user.role === "buyer");
  const addressesQuery = useAddresses(enabled);
  const addAddress = useAddAddress();
  const updateAddress = useUpdateAddress();
  const deleteAddress = useDeleteAddress();
  const setDefaultAddress = useSetDefaultAddress();

  async function handleSubmit(values: AddressInput) {
    if (editing) {
      await updateAddress.mutateAsync({ id: editing.id, input: values });
    } else {
      await addAddress.mutateAsync(values);
    }
    setEditing(null);
    setFormOpen(false);
  }

  function handleCancel() {
    setEditing(null);
    setFormOpen(false);
  }

  if (!user || user.role !== "buyer") return null;

  return (
    <PageShell maxWidth="sm">
      <SectionHeader
        as="h1"
        title="Địa chỉ giao hàng của bạn"
        action={
          !formOpen && (
            <Button
              size="sm"
              onClick={() => {
                setEditing(null);
                setFormOpen(true);
              }}
            >
              Thêm địa chỉ mới
            </Button>
          )
        }
      />

      {addressesQuery.isPending && <LoadingState className="mt-6" rows={2} />}
      {addressesQuery.error && (
        <ErrorState message="Không thể tải danh sách địa chỉ." onRetry={addressesQuery.refetch} />
      )}
      {addressesQuery.data?.length === 0 && (
        <EmptyState className="mt-6" title="Bạn chưa lưu địa chỉ nào" />
      )}

      {addressesQuery.data && addressesQuery.data.length > 0 && (
        <ul className="mt-6 flex flex-col gap-3">
          {addressesQuery.data.map((a) => (
            <li key={a.id}>
              <Card className={cn(a.is_default && "border-primary")}>
                <CardContent className="flex items-start justify-between gap-4">
                  <div>
                    <p className="flex items-center gap-2 font-medium">
                      {a.recipient_name}
                      {a.is_default && <Badge variant="success">Mặc định</Badge>}
                    </p>
                    <p className="text-sm text-muted-foreground">{a.phone}</p>
                    <p className="text-sm text-muted-foreground">
                      {a.street_address}, {a.ward}, {a.district}, {a.province}
                    </p>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label="Sửa địa chỉ"
                      onClick={() => {
                        setEditing(a);
                        setFormOpen(true);
                      }}
                    >
                      <Pencil className="size-4" />
                    </Button>
                    {!a.is_default && (
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label="Đặt làm mặc định"
                        disabled={setDefaultAddress.isPending}
                        onClick={() => setDefaultAddress.mutate(a.id)}
                      >
                        <Star className="size-4" />
                      </Button>
                    )}
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label="Xóa địa chỉ"
                          className="text-destructive hover:text-destructive"
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>Xóa địa chỉ này?</AlertDialogTitle>
                          <AlertDialogDescription>
                            Không thể hoàn tác. Bạn sẽ cần thêm lại nếu đổi ý.
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel>Hủy</AlertDialogCancel>
                          <AlertDialogAction onClick={() => deleteAddress.mutate(a.id)}>
                            Xóa
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </div>
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}

      {formOpen && (
        <Card className="mt-8">
          <CardHeader>
            <CardTitle className="text-base">
              {editing ? "Sửa địa chỉ" : "Thêm địa chỉ mới"}
            </CardTitle>
          </CardHeader>
          <CardContent>
            <AddressForm
              key={editing?.id ?? "new"}
              defaultValues={editing ?? undefined}
              onSubmit={handleSubmit}
              onCancel={handleCancel}
              isSubmitting={addAddress.isPending || updateAddress.isPending}
              submitLabel={editing ? "Lưu thay đổi" : "Thêm địa chỉ"}
            />
          </CardContent>
        </Card>
      )}
    </PageShell>
  );
}
