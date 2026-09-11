"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

const emptyForm: api.AddressInput = {
  recipient_name: "",
  phone: "",
  province: "",
  district: "",
  ward: "",
  street_address: "",
};

export default function AddressesPage() {
  const { user, isReady, callWithAuth } = useAuth();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [form, setForm] = useState<api.AddressInput>(emptyForm);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  useEffect(() => {
    if (isReady && (!user || user.role !== "buyer")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  const addressesQuery = useQuery({
    queryKey: ["buyer-addresses"],
    queryFn: () => callWithAuth((token) => api.listBuyerAddresses(token)),
    enabled: Boolean(user && user.role === "buyer"),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["buyer-addresses"] });
  }

  function startEdit(a: api.BuyerAddress) {
    setEditingId(a.id);
    setForm({
      recipient_name: a.recipient_name,
      phone: a.phone,
      province: a.province,
      district: a.district,
      ward: a.ward,
      street_address: a.street_address,
    });
  }

  function cancelEdit() {
    setEditingId(null);
    setForm(emptyForm);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      if (editingId) {
        await callWithAuth((token) => api.updateBuyerAddress(token, editingId, form));
      } else {
        await callWithAuth((token) => api.addBuyerAddress(token, form));
      }
      setForm(emptyForm);
      setEditingId(null);
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not save this address.");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleDelete(id: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.deleteBuyerAddress(token, id));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not delete this address.");
    }
  }

  async function handleSetDefault(id: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.setDefaultBuyerAddress(token, id));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not set this address as default.");
    }
  }

  if (!user || user.role !== "buyer") return null;

  return (
    <main className="mx-auto max-w-2xl px-6 py-10">
      <h1 className="text-2xl font-semibold text-slate-900">Your shipping addresses</h1>
      {error && <p className="mt-4 text-sm text-red-600">{error}</p>}

      <ul className="mt-6 space-y-3">
        {addressesQuery.data?.map((a) => (
          <li key={a.id} className="rounded border border-slate-200 bg-white p-4">
            <div className="flex items-start justify-between">
              <div>
                <p className="font-medium text-slate-900">
                  {a.recipient_name} {a.is_default && <span className="ml-1 text-xs text-emerald-600">(default)</span>}
                </p>
                <p className="text-sm text-slate-600">{a.phone}</p>
                <p className="text-sm text-slate-600">
                  {a.street_address}, {a.ward}, {a.district}, {a.province}
                </p>
              </div>
              <div className="flex flex-col items-end gap-1 text-sm">
                <button onClick={() => startEdit(a)} className="text-slate-600 hover:underline">
                  Edit
                </button>
                {!a.is_default && (
                  <button onClick={() => handleSetDefault(a.id)} className="text-slate-600 hover:underline">
                    Set default
                  </button>
                )}
                <button onClick={() => handleDelete(a.id)} className="text-red-600 hover:underline">
                  Delete
                </button>
              </div>
            </div>
          </li>
        ))}
        {addressesQuery.data?.length === 0 && (
          <p className="text-sm text-slate-500">No saved addresses yet.</p>
        )}
      </ul>

      <div className="mt-8 rounded border border-slate-200 bg-white p-4">
        <h2 className="text-sm font-medium text-slate-700">
          {editingId ? "Edit address" : "Add a new address"}
        </h2>
        <form onSubmit={handleSubmit} className="mt-3 grid gap-3 sm:grid-cols-2">
          <input
            value={form.recipient_name}
            onChange={(e) => setForm({ ...form, recipient_name: e.target.value })}
            placeholder="Recipient name"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          />
          <input
            value={form.phone}
            onChange={(e) => setForm({ ...form, phone: e.target.value })}
            placeholder="Phone number"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          />
          <input
            value={form.province}
            onChange={(e) => setForm({ ...form, province: e.target.value })}
            placeholder="Province"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          />
          <input
            value={form.district}
            onChange={(e) => setForm({ ...form, district: e.target.value })}
            placeholder="District"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          />
          <input
            value={form.ward}
            onChange={(e) => setForm({ ...form, ward: e.target.value })}
            placeholder="Ward"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          />
          <input
            value={form.street_address}
            onChange={(e) => setForm({ ...form, street_address: e.target.value })}
            placeholder="Street address"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm sm:col-span-2"
          />
          <div className="flex gap-2 sm:col-span-2">
            <button
              type="submit"
              disabled={isSubmitting}
              className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
            >
              {isSubmitting ? "Saving…" : editingId ? "Save changes" : "Add address"}
            </button>
            {editingId && (
              <button
                type="button"
                onClick={cancelEdit}
                className="rounded border border-slate-300 px-4 py-2 text-sm text-slate-700"
              >
                Cancel
              </button>
            )}
          </div>
        </form>
      </div>
    </main>
  );
}
