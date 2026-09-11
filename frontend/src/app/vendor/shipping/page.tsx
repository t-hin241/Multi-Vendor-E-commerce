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

export default function VendorShippingPage() {
  const { user, isReady } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (isReady && (!user || user.role !== "vendor")) {
      router.replace("/login");
    }
  }, [isReady, user, router]);

  if (!user || user.role !== "vendor") return null;

  return (
    <main className="mx-auto max-w-2xl px-6 py-10">
      <h1 className="text-2xl font-semibold text-slate-900">Shipping settings</h1>

      <section className="mt-8">
        <ShippingMethods />
      </section>

      <section className="mt-10">
        <WarehouseAddresses />
      </section>
    </main>
  );
}

// ShippingMethods lets a vendor enable one of admin's active carriers for
// their own shop and choose which one is the default — the system uses
// the default carrier automatically to quote and create a shipment at
// checkout, with no buyer choice involved.
function ShippingMethods() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [carrierId, setCarrierId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const carriersQuery = useQuery({ queryKey: ["active-carriers"], queryFn: () => api.listActiveCarriers() });
  const methodsQuery = useQuery({
    queryKey: ["vendor-shipping-methods"],
    queryFn: () => callWithAuth((token) => api.listMyShippingMethods(token)),
  });

  const carriers = carriersQuery.data ?? [];
  const methods = methodsQuery.data ?? [];
  const enabledCarrierIds = new Set(methods.map((m) => m.carrier_id));
  const availableCarriers = carriers.filter((c) => !enabledCarrierIds.has(c.id));

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-shipping-methods"] });
  }

  async function handleEnable(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await callWithAuth((token) => api.enableShippingMethod(token, carrierId));
      setCarrierId("");
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not enable this carrier.");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleSetDefault(methodId: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.setDefaultShippingMethod(token, methodId));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not set this carrier as default.");
    }
  }

  async function handleSetActive(methodId: string, isActive: boolean) {
    setError(null);
    try {
      await callWithAuth((token) => api.setShippingMethodActive(token, methodId, isActive));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update this carrier.");
    }
  }

  function carrierName(id: string) {
    return carriers.find((c) => c.id === id)?.name ?? id;
  }

  return (
    <div>
      <h2 className="text-lg font-medium text-slate-900">Shipping methods</h2>
      <p className="mt-1 text-sm text-slate-500">
        Enable the carriers you ship with. Your default carrier is used automatically to quote and
        create a shipment when a buyer checks out.
      </p>
      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}

      <ul className="mt-4 space-y-2">
        {methods.map((m) => (
          <li
            key={m.id}
            className="flex items-center justify-between rounded border border-slate-200 bg-white p-3"
          >
            <span className="text-sm text-slate-900">
              {carrierName(m.carrier_id)}
              {m.is_default && <span className="ml-2 text-xs text-emerald-600">(default)</span>}
              {!m.is_active && <span className="ml-2 text-xs text-slate-400">(inactive)</span>}
            </span>
            <div className="flex gap-3 text-sm">
              {!m.is_default && m.is_active && (
                <button onClick={() => handleSetDefault(m.id)} className="text-slate-600 hover:underline">
                  Set default
                </button>
              )}
              <button
                onClick={() => handleSetActive(m.id, !m.is_active)}
                className="text-slate-600 hover:underline"
              >
                {m.is_active ? "Deactivate" : "Activate"}
              </button>
            </div>
          </li>
        ))}
        {methods.length === 0 && <p className="text-sm text-slate-500">No shipping methods enabled yet.</p>}
      </ul>

      {availableCarriers.length > 0 && (
        <form onSubmit={handleEnable} className="mt-4 flex gap-2">
          <select
            value={carrierId}
            onChange={(e) => setCarrierId(e.target.value)}
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          >
            <option value="">Choose a carrier to enable…</option>
            {availableCarriers.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
          <button
            type="submit"
            disabled={isSubmitting}
            className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          >
            Enable
          </button>
        </form>
      )}
    </div>
  );
}

// WarehouseAddresses is where the vendor ships from — mirrors the buyer's
// own address book (frontend/src/app/addresses/page.tsx). It's kept
// separate from shipping-fee calculation (which is destination-only in
// this pass) and is mainly for the vendor's own operational record.
function WarehouseAddresses() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [form, setForm] = useState<api.AddressInput>(emptyForm);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const addressesQuery = useQuery({
    queryKey: ["vendor-addresses"],
    queryFn: () => callWithAuth((token) => api.listVendorAddresses(token)),
  });

  async function refresh() {
    await queryClient.invalidateQueries({ queryKey: ["vendor-addresses"] });
  }

  function startEdit(a: api.VendorAddress) {
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
        await callWithAuth((token) => api.updateVendorAddress(token, editingId, form));
      } else {
        await callWithAuth((token) => api.addVendorAddress(token, form));
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
      await callWithAuth((token) => api.deleteVendorAddress(token, id));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not delete this address.");
    }
  }

  async function handleSetDefault(id: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.setDefaultVendorAddress(token, id));
      await refresh();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not set this address as default.");
    }
  }

  return (
    <div>
      <h2 className="text-lg font-medium text-slate-900">Warehouse addresses</h2>
      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}

      <ul className="mt-4 space-y-3">
        {addressesQuery.data?.map((a) => (
          <li key={a.id} className="rounded border border-slate-200 bg-white p-4">
            <div className="flex items-start justify-between">
              <div>
                <p className="font-medium text-slate-900">
                  {a.recipient_name}
                  {a.is_default && <span className="ml-1 text-xs text-emerald-600">(default)</span>}
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
          <p className="text-sm text-slate-500">No warehouse addresses yet.</p>
        )}
      </ul>

      <div className="mt-4 rounded border border-slate-200 bg-white p-4">
        <h3 className="text-sm font-medium text-slate-700">
          {editingId ? "Edit address" : "Add a warehouse address"}
        </h3>
        <form onSubmit={handleSubmit} className="mt-3 grid gap-3 sm:grid-cols-2">
          <input
            value={form.recipient_name}
            onChange={(e) => setForm({ ...form, recipient_name: e.target.value })}
            placeholder="Contact name"
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
    </div>
  );
}
