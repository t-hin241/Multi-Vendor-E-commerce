"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

const VENDOR_STATUS_OPTIONS = ["pending", "approved", "rejected", ""] as const;
const PRODUCT_STATUS_OPTIONS = ["pending_review", "approved", "rejected", ""] as const;

export default function AdminDashboardPage() {
  return (
    <main className="mx-auto max-w-3xl px-6 py-10">
      <h1 className="text-xl font-semibold text-slate-900">Admin Console</h1>

      <section className="mt-8">
        <VendorModeration />
      </section>

      <section className="mt-10">
        <ProductModeration />
      </section>

      <section className="mt-10">
        <OrderIntervention />
      </section>

      <section className="mt-10">
        <CategoryManager />
      </section>

      <section className="mt-10">
        <AttributeManager />
      </section>

      <section className="mt-10">
        <CommissionRuleManager />
      </section>

      <section className="mt-10">
        <ShippingConfigManager />
      </section>
    </main>
  );
}

// ShippingConfigManager lets admin manage carriers, shipping zones (and
// which provinces belong to each) and the fee rule for each carrier/zone
// pair — the automatic shipment-fee calculation at checkout depends on all
// three being configured. Fee rules are insert-only, mirroring
// CommissionRuleManager above: setting a new one never edits history, so a
// shipment already created keeps the fee it was quoted.
function ShippingConfigManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();

  const carriersQuery = useQuery({ queryKey: ["carriers"], queryFn: () => callWithAuth((token) => api.listCarriers(token)) });
  const zonesQuery = useQuery({ queryKey: ["zones"], queryFn: () => callWithAuth((token) => api.listZones(token)) });
  const feeRulesQuery = useQuery({ queryKey: ["fee-rules"], queryFn: () => callWithAuth((token) => api.listFeeRules(token)) });

  const [carrierName, setCarrierName] = useState("");
  const [carrierCode, setCarrierCode] = useState("");
  const [zoneName, setZoneName] = useState("");
  const [zoneCode, setZoneCode] = useState("");
  const [provinceZoneId, setProvinceZoneId] = useState("");
  const [provinceCode, setProvinceCode] = useState("");
  const [ruleCarrierId, setRuleCarrierId] = useState("");
  const [ruleZoneId, setRuleZoneId] = useState("");
  const [baseFee, setBaseFee] = useState("");
  const [freeWeightKg, setFreeWeightKg] = useState("0");
  const [extraFeePerKg, setExtraFeePerKg] = useState("0");
  const [error, setError] = useState<string | null>(null);

  const carriers = carriersQuery.data ?? [];
  const zones = zonesQuery.data ?? [];
  const feeRules = feeRulesQuery.data ?? [];

  async function handleCreateCarrier(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await callWithAuth((token) => api.createCarrier(token, carrierName, carrierCode));
      setCarrierName("");
      setCarrierCode("");
      await queryClient.invalidateQueries({ queryKey: ["carriers"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not create carrier.");
    }
  }

  async function handleCreateZone(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await callWithAuth((token) => api.createZone(token, zoneName, zoneCode));
      setZoneName("");
      setZoneCode("");
      await queryClient.invalidateQueries({ queryKey: ["zones"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not create zone.");
    }
  }

  async function handleAddProvince(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await callWithAuth((token) => api.addProvinceToZone(token, provinceZoneId, provinceCode));
      setProvinceCode("");
      await queryClient.invalidateQueries({ queryKey: ["zone-provinces"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not map province to zone.");
    }
  }

  async function handleSetFeeRule(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const base = Number(baseFee);
    const freeKg = Number(freeWeightKg);
    const perKg = Number(extraFeePerKg);
    if (!ruleCarrierId || !ruleZoneId || Number.isNaN(base) || Number.isNaN(freeKg) || Number.isNaN(perKg)) {
      setError("Fill in carrier, zone and valid numbers for the fee.");
      return;
    }
    try {
      await callWithAuth((token) =>
        api.setFeeRule(token, ruleCarrierId, ruleZoneId, Math.round(base), Math.round(freeKg * 1000), Math.round(perKg)),
      );
      setBaseFee("");
      await queryClient.invalidateQueries({ queryKey: ["fee-rules"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not set fee rule.");
    }
  }

  function carrierName_(id: string) {
    return carriers.find((c) => c.id === id)?.name ?? id;
  }
  function zoneName_(id: string) {
    return zones.find((z) => z.id === id)?.name ?? id;
  }

  return (
    <div>
      <h2 className="text-lg font-medium text-slate-900">Shipping configuration</h2>
      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}

      <div className="mt-4 grid gap-6 sm:grid-cols-2">
        <div>
          <h3 className="text-sm font-medium text-slate-700">Carriers</h3>
          <form onSubmit={handleCreateCarrier} className="mt-2 flex gap-2">
            <input
              value={carrierName}
              onChange={(e) => setCarrierName(e.target.value)}
              placeholder="Name (e.g. GHN)"
              required
              className="w-full rounded border border-slate-300 px-2 py-1 text-sm"
            />
            <input
              value={carrierCode}
              onChange={(e) => setCarrierCode(e.target.value)}
              placeholder="Code"
              required
              className="w-32 rounded border border-slate-300 px-2 py-1 text-sm"
            />
            <button type="submit" className="rounded bg-slate-900 px-3 py-1 text-sm text-white">
              Add
            </button>
          </form>
          <ul className="mt-2 space-y-1 text-sm text-slate-700">
            {carriers.map((c) => (
              <li key={c.id}>
                {c.name} ({c.code}) {c.is_active ? "" : "— inactive"}
              </li>
            ))}
          </ul>
        </div>

        <div>
          <h3 className="text-sm font-medium text-slate-700">Zones</h3>
          <form onSubmit={handleCreateZone} className="mt-2 flex gap-2">
            <input
              value={zoneName}
              onChange={(e) => setZoneName(e.target.value)}
              placeholder="Name (e.g. Miền Bắc)"
              required
              className="w-full rounded border border-slate-300 px-2 py-1 text-sm"
            />
            <input
              value={zoneCode}
              onChange={(e) => setZoneCode(e.target.value)}
              placeholder="Code"
              required
              className="w-32 rounded border border-slate-300 px-2 py-1 text-sm"
            />
            <button type="submit" className="rounded bg-slate-900 px-3 py-1 text-sm text-white">
              Add
            </button>
          </form>
          <ul className="mt-2 space-y-1 text-sm text-slate-700">
            {zones.map((z) => (
              <li key={z.id}>
                {z.name} ({z.code})
              </li>
            ))}
          </ul>
        </div>
      </div>

      <div className="mt-6">
        <h3 className="text-sm font-medium text-slate-700">Map a province to a zone</h3>
        <form onSubmit={handleAddProvince} className="mt-2 flex gap-2">
          <select
            value={provinceZoneId}
            onChange={(e) => setProvinceZoneId(e.target.value)}
            required
            className="rounded border border-slate-300 px-2 py-1 text-sm"
          >
            <option value="">Zone…</option>
            {zones.map((z) => (
              <option key={z.id} value={z.id}>
                {z.name}
              </option>
            ))}
          </select>
          <input
            value={provinceCode}
            onChange={(e) => setProvinceCode(e.target.value)}
            placeholder="Province code (e.g. HN)"
            required
            className="w-48 rounded border border-slate-300 px-2 py-1 text-sm"
          />
          <button type="submit" className="rounded bg-slate-900 px-3 py-1 text-sm text-white">
            Map
          </button>
        </form>
      </div>

      <div className="mt-6">
        <h3 className="text-sm font-medium text-slate-700">Fee rules</h3>
        <form onSubmit={handleSetFeeRule} className="mt-2 flex flex-wrap items-end gap-2">
          <select
            value={ruleCarrierId}
            onChange={(e) => setRuleCarrierId(e.target.value)}
            required
            className="rounded border border-slate-300 px-2 py-1 text-sm"
          >
            <option value="">Carrier…</option>
            {carriers.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
          <select
            value={ruleZoneId}
            onChange={(e) => setRuleZoneId(e.target.value)}
            required
            className="rounded border border-slate-300 px-2 py-1 text-sm"
          >
            <option value="">Zone…</option>
            {zones.map((z) => (
              <option key={z.id} value={z.id}>
                {z.name}
              </option>
            ))}
          </select>
          <label className="flex flex-col text-xs text-slate-600">
            Base fee
            <input
              value={baseFee}
              onChange={(e) => setBaseFee(e.target.value)}
              placeholder="VND"
              required
              className="w-28 rounded border border-slate-300 px-2 py-1 text-sm"
            />
          </label>
          <label className="flex flex-col text-xs text-slate-600">
            Free weight (kg)
            <input
              value={freeWeightKg}
              onChange={(e) => setFreeWeightKg(e.target.value)}
              className="w-24 rounded border border-slate-300 px-2 py-1 text-sm"
            />
          </label>
          <label className="flex flex-col text-xs text-slate-600">
            Extra fee / kg
            <input
              value={extraFeePerKg}
              onChange={(e) => setExtraFeePerKg(e.target.value)}
              className="w-28 rounded border border-slate-300 px-2 py-1 text-sm"
            />
          </label>
          <button type="submit" className="rounded bg-slate-900 px-3 py-1.5 text-sm text-white">
            Set current rule
          </button>
        </form>
        <ul className="mt-3 space-y-1 text-sm text-slate-700">
          {feeRules.map((r) => (
            <li key={r.id}>
              {carrierName_(r.carrier_id)} → {zoneName_(r.zone_id)}: base {r.base_fee_amount.toLocaleString("vi-VN")} VND,
              free up to {(r.free_weight_grams / 1000).toFixed(1)}kg, +{r.extra_fee_per_kg.toLocaleString("vi-VN")}/kg
              after (v{r.version})
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

// CommissionRuleManager lets admin set the marketplace's commission
// percentage. Rules are insert-only on the backend — setting a new one
// never edits history, so past vendor orders keep the rate they were
// snapshotted with when paid.
function CommissionRuleManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [percent, setPercent] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const rulesQuery = useQuery({
    queryKey: ["commission-rules"],
    queryFn: () => callWithAuth((token) => api.listCommissionRules(token)),
  });

  const current = rulesQuery.data?.[0];

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    const percentValue = Number(percent);
    if (Number.isNaN(percentValue) || percentValue < 0 || percentValue > 100) {
      setError("Enter a percentage between 0 and 100.");
      return;
    }
    setIsSubmitting(true);
    try {
      await callWithAuth((token) => api.setCommissionRule(token, Math.round(percentValue * 100)));
      setPercent("");
      await queryClient.invalidateQueries({ queryKey: ["commission-rules"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update the commission rate.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div>
      <h2 className="text-lg font-medium text-slate-900">Commission</h2>
      {current && (
        <p className="mt-1 text-sm text-slate-600">
          Current rate:{" "}
          <span className="font-medium text-slate-900">{(current.rate_bps / 100).toFixed(2)}%</span>
        </p>
      )}

      <form onSubmit={handleSubmit} className="mt-3 flex items-end gap-3">
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          New rate (%)
          <input
            type="number"
            step="0.01"
            min={0}
            max={100}
            value={percent}
            onChange={(e) => setPercent(e.target.value)}
            required
            className="w-32 rounded border border-slate-300 px-3 py-2 text-sm"
          />
        </label>
        <button
          type="submit"
          disabled={isSubmitting}
          className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
        >
          {isSubmitting ? "Saving…" : "Set rate"}
        </button>
      </form>
      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}
    </div>
  );
}

const ORDER_STATUS_OPTIONS = [
  "",
  "pending_payment",
  "paid",
  "processing",
  "shipped",
  "completed",
  "cancelled",
  "refunded",
] as const;

function formatMoney(amount: number, currency: string) {
  return `${amount.toLocaleString("vi-VN")} ${currency}`;
}

// OrderIntervention is admin's moderation view of orders: browsing across
// every buyer, and — for a dispute or a stuck order — cancelling or
// refunding it outside the normal vendor-driven fulfillment path. Ordinary
// progress (processing/shipped/completed) stays vendor-driven; admin only
// ever moves an order to cancelled or refunded, same as the backend enforces.
function OrderIntervention() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("");
  const [error, setError] = useState<string | null>(null);

  const ordersQuery = useQuery({
    queryKey: ["admin-orders", status],
    queryFn: () =>
      callWithAuth((token) => api.listAdminOrders(token, { status: status || undefined })),
  });

  async function handleTransition(orderId: string, target: "cancelled" | "refunded") {
    const reason = window.prompt(`Reason to mark this order ${target}:`);
    if (!reason) return;
    setError(null);
    try {
      await callWithAuth((token) => api.adminTransitionOrder(token, orderId, target, reason));
      await queryClient.invalidateQueries({ queryKey: ["admin-orders"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not update this order.");
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-medium text-slate-900">Orders</h2>
        <select
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className="rounded border border-slate-300 px-2 py-1 text-sm"
        >
          {ORDER_STATUS_OPTIONS.map((s) => (
            <option key={s} value={s}>
              {s === "" ? "All" : s.replace("_", " ")}
            </option>
          ))}
        </select>
      </div>

      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}
      {ordersQuery.data?.length === 0 && (
        <p className="mt-3 text-sm text-slate-500">Nothing here.</p>
      )}

      <ul className="mt-3 divide-y divide-slate-200 rounded border border-slate-200 bg-white">
        {ordersQuery.data?.map((o) => (
          <li key={o.id} className="flex items-center justify-between gap-3 p-4">
            <div>
              <p className="font-medium text-slate-900">Order #{o.id.slice(0, 8)}</p>
              <p className="text-sm text-slate-600">{formatMoney(o.total_amount, o.currency)}</p>
              <p className="text-sm capitalize text-slate-500">{o.status.replace("_", " ")}</p>
            </div>
            <div className="flex gap-2">
              {o.status === "pending_payment" && (
                <button
                  onClick={() => handleTransition(o.id, "cancelled")}
                  className="rounded bg-red-600 px-3 py-1.5 text-sm text-white"
                >
                  Cancel
                </button>
              )}
              {["paid", "processing", "shipped", "completed"].includes(o.status) && (
                <button
                  onClick={() => handleTransition(o.id, "refunded")}
                  className="rounded bg-amber-600 px-3 py-1.5 text-sm text-white"
                >
                  Refund
                </button>
              )}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

function StatusFilter({
  status,
  onChange,
  options,
}: {
  status: string;
  onChange: (s: string) => void;
  options: readonly string[];
}) {
  return (
    <select
      value={status}
      onChange={(e) => onChange(e.target.value)}
      className="rounded border border-slate-300 px-2 py-1 text-sm"
    >
      {options.map((s) => (
        <option key={s} value={s}>
          {s === "" ? "All" : s.replace("_", " ")}
        </option>
      ))}
    </select>
  );
}

function VendorModeration() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("pending");
  const [error, setError] = useState<string | null>(null);

  const vendorsQuery = useQuery({
    queryKey: ["admin-vendor-applications", status],
    queryFn: () =>
      callWithAuth((token) => api.listVendorApplications(token, { status: status || undefined })),
  });

  async function handleApprove(id: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.approveVendor(token, id));
      await queryClient.invalidateQueries({ queryKey: ["admin-vendor-applications"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not approve vendor.");
    }
  }

  async function handleReject(id: string) {
    const reason = window.prompt("Rejection reason:");
    if (!reason) return;
    setError(null);
    try {
      await callWithAuth((token) => api.rejectVendor(token, id, reason));
      await queryClient.invalidateQueries({ queryKey: ["admin-vendor-applications"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not reject vendor.");
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-medium text-slate-900">Vendor applications</h2>
        <StatusFilter status={status} onChange={setStatus} options={VENDOR_STATUS_OPTIONS} />
      </div>

      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}
      {vendorsQuery.data?.length === 0 && (
        <p className="mt-3 text-sm text-slate-500">Nothing here.</p>
      )}

      <ul className="mt-3 divide-y divide-slate-200 rounded border border-slate-200 bg-white">
        {vendorsQuery.data?.map((v) => (
          <li key={v.id} className="flex items-center justify-between gap-3 p-4">
            <div>
              <p className="font-medium text-slate-900">{v.shop_name}</p>
              <p className="text-sm text-slate-600">{v.description}</p>
              <p className="text-sm capitalize text-slate-500">{v.status}</p>
            </div>
            {v.status === "pending" && (
              <div className="flex gap-2">
                <button
                  onClick={() => handleApprove(v.id)}
                  className="rounded bg-emerald-600 px-3 py-1.5 text-sm text-white"
                >
                  Approve
                </button>
                <button
                  onClick={() => handleReject(v.id)}
                  className="rounded bg-red-600 px-3 py-1.5 text-sm text-white"
                >
                  Reject
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function ProductModeration() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [status, setStatus] = useState("pending_review");
  const [error, setError] = useState<string | null>(null);

  const productsQuery = useQuery({
    queryKey: ["admin-products", status],
    queryFn: () =>
      callWithAuth((token) =>
        api.listProductsForModeration(token, { status: status || undefined }),
      ),
  });

  async function handleApprove(id: string) {
    setError(null);
    try {
      await callWithAuth((token) => api.approveProduct(token, id));
      await queryClient.invalidateQueries({ queryKey: ["admin-products"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not approve product.");
    }
  }

  async function handleReject(id: string) {
    const reason = window.prompt("Rejection reason:");
    if (!reason) return;
    setError(null);
    try {
      await callWithAuth((token) => api.rejectProduct(token, id, reason));
      await queryClient.invalidateQueries({ queryKey: ["admin-products"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not reject product.");
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-medium text-slate-900">Product moderation</h2>
        <StatusFilter status={status} onChange={setStatus} options={PRODUCT_STATUS_OPTIONS} />
      </div>

      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}
      {productsQuery.data?.length === 0 && (
        <p className="mt-3 text-sm text-slate-500">Nothing here.</p>
      )}

      <ul className="mt-3 divide-y divide-slate-200 rounded border border-slate-200 bg-white">
        {productsQuery.data?.map((p) => (
          <li key={p.id} className="flex items-center justify-between gap-3 p-4">
            <div>
              <p className="font-medium text-slate-900">{p.name}</p>
              <p className="text-sm text-slate-600">
                {p.price_amount.toLocaleString("vi-VN")} {p.currency}
              </p>
              <p className="text-sm capitalize text-slate-500">{p.status}</p>
            </div>
            {p.status === "pending_review" && (
              <div className="flex gap-2">
                <button
                  onClick={() => handleApprove(p.id)}
                  className="rounded bg-emerald-600 px-3 py-1.5 text-sm text-white"
                >
                  Approve
                </button>
                <button
                  onClick={() => handleReject(p.id)}
                  className="rounded bg-red-600 px-3 py-1.5 text-sm text-white"
                >
                  Reject
                </button>
              </div>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

const CATEGORY_LEVEL_LABELS = ["", "Danh mục cấp 1 (main-category)", "Danh mục cấp 2 (category)", "Danh mục cấp 3 (sub-category)"];

function CategoryManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [mainId, setMainId] = useState("");
  const [midId, setMidId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const categoriesQuery = useQuery({ queryKey: ["categories"], queryFn: api.listCategories });
  const categories = categoriesQuery.data ?? [];
  const mains = categories.filter((c) => c.level === 1);
  const childrenOf = (parentId: string) => categories.filter((c) => c.parent_id === parentId);

  const midOptions = mainId ? childrenOf(mainId) : [];
  const levelToCreate = midId ? 3 : mainId ? 2 : 1;
  const parentId = midId || mainId || undefined;

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await callWithAuth((token) => api.createCategory(token, name, parentId));
      setName("");
      await queryClient.invalidateQueries({ queryKey: ["categories"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not create category.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div>
      <h2 className="text-lg font-medium text-slate-900">Categories</h2>

      <div className="mt-3 flex gap-3">
        <select
          value={mainId}
          onChange={(e) => {
            setMainId(e.target.value);
            setMidId("");
          }}
          className="rounded border border-slate-300 px-3 py-2 text-sm"
        >
          <option value="">— Tạo mới danh mục cấp 1 —</option>
          {mains.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        {mainId && (
          <select
            value={midId}
            onChange={(e) => setMidId(e.target.value)}
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          >
            <option value="">— Tạo mới danh mục cấp 2 —</option>
            {midOptions.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        )}
      </div>
      <p className="mt-1 text-xs text-slate-500">Sẽ tạo: {CATEGORY_LEVEL_LABELS[levelToCreate]}</p>

      <form onSubmit={handleSubmit} className="mt-3 flex gap-3">
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="New category name"
          required
          className="rounded border border-slate-300 px-3 py-2 text-sm"
        />
        <button
          type="submit"
          disabled={isSubmitting}
          className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
        >
          Add
        </button>
      </form>
      {error && <p className="mt-2 text-sm text-red-600">{error}</p>}

      <ul className="mt-4 space-y-2">
        {mains.map((main) => (
          <li key={main.id}>
            <span className="text-sm font-medium text-slate-900">{main.name}</span>
            <ul className="mt-1 ml-4 space-y-1">
              {childrenOf(main.id).map((mid) => (
                <li key={mid.id}>
                  <span className="text-sm text-slate-700">{mid.name}</span>
                  <ul className="mt-1 ml-4 space-y-1">
                    {childrenOf(mid.id).map((sub) => (
                      <li key={sub.id} className="text-xs text-slate-500">
                        {sub.name}
                      </li>
                    ))}
                  </ul>
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
    </div>
  );
}

const ATTRIBUTE_DATA_TYPES: api.AttributeDataType[] = ["text", "number", "boolean", "select", "multi_select"];

// AttributeManager lets admin build the Attribute Management System's data:
// define attributes (+ options for select/multi_select ones), then assign a
// versioned rule to a category node at any of its 3 levels. A rule set on a
// main-category flows down to its children unless a deeper node overrides
// it (see the resolved-template preview below the rule form, which calls
// the same endpoint the vendor's add-product form renders itself from).
function AttributeManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();

  const attributesQuery = useQuery({
    queryKey: ["attributes-admin"],
    queryFn: () => callWithAuth((token) => api.listAttributes(token)),
  });
  const categoriesQuery = useQuery({ queryKey: ["categories"], queryFn: api.listCategories });
  const categories = categoriesQuery.data ?? [];
  const mains = categories.filter((c) => c.level === 1);
  const childrenOf = (parentId: string) => categories.filter((c) => c.parent_id === parentId);

  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [dataType, setDataType] = useState<api.AttributeDataType>("text");
  const [unit, setUnit] = useState("");
  const [isVariantDefining, setIsVariantDefining] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [isCreating, setIsCreating] = useState(false);

  async function handleCreateAttribute(e: React.FormEvent) {
    e.preventDefault();
    setCreateError(null);
    setIsCreating(true);
    try {
      await callWithAuth((token) =>
        api.createAttribute(token, code, name, dataType, unit || undefined, isVariantDefining),
      );
      setCode("");
      setName("");
      setUnit("");
      setDataType("text");
      setIsVariantDefining(false);
      await queryClient.invalidateQueries({ queryKey: ["attributes-admin"] });
    } catch (err) {
      setCreateError(err instanceof api.ApiError ? err.message : "Could not create attribute.");
    } finally {
      setIsCreating(false);
    }
  }

  const [ruleMainId, setRuleMainId] = useState("");
  const [ruleMidId, setRuleMidId] = useState("");
  const [ruleSubId, setRuleSubId] = useState("");
  const ruleMidOptions = ruleMainId ? childrenOf(ruleMainId) : [];
  const ruleSubOptions = ruleMidId ? childrenOf(ruleMidId) : [];
  const targetCategoryId = ruleSubId || ruleMidId || ruleMainId;

  const [ruleAttributeId, setRuleAttributeId] = useState("");
  const [ruleRequired, setRuleRequired] = useState(false);
  const [ruleExcluded, setRuleExcluded] = useState(false);
  const [rulePosition, setRulePosition] = useState(0);
  const [ruleError, setRuleError] = useState<string | null>(null);
  const [isSettingRule, setIsSettingRule] = useState(false);

  const templateQuery = useQuery({
    queryKey: ["attribute-template", targetCategoryId],
    queryFn: () => api.getAttributeTemplate(targetCategoryId),
    enabled: !!targetCategoryId,
  });

  async function handleSetRule(e: React.FormEvent) {
    e.preventDefault();
    setRuleError(null);
    setIsSettingRule(true);
    try {
      await callWithAuth((token) =>
        api.setCategoryAttributeRule(
          token,
          targetCategoryId,
          ruleAttributeId,
          ruleRequired,
          ruleExcluded,
          rulePosition,
        ),
      );
      await queryClient.invalidateQueries({ queryKey: ["attribute-template", targetCategoryId] });
    } catch (err) {
      setRuleError(err instanceof api.ApiError ? err.message : "Could not set the attribute rule.");
    } finally {
      setIsSettingRule(false);
    }
  }

  return (
    <div>
      <h2 className="text-lg font-medium text-slate-900">Attributes</h2>

      <form onSubmit={handleCreateAttribute} className="mt-3 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Code
          <input
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="mau_sac"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          />
        </label>
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Name
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Màu sắc"
            required
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          />
        </label>
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Data type
          <select
            value={dataType}
            onChange={(e) => {
              const next = e.target.value as api.AttributeDataType;
              setDataType(next);
              if (next !== "select") setIsVariantDefining(false);
            }}
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          >
            {ATTRIBUTE_DATA_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </label>
        <label
          className={`flex items-center gap-2 text-sm ${dataType === "select" ? "text-slate-700" : "text-slate-300"}`}
        >
          <input
            type="checkbox"
            checked={isVariantDefining}
            disabled={dataType !== "select"}
            onChange={(e) => setIsVariantDefining(e.target.checked)}
          />
          Use for variants
        </label>
        <label className="flex flex-col gap-1 text-sm text-slate-700">
          Unit (optional)
          <input
            value={unit}
            onChange={(e) => setUnit(e.target.value)}
            placeholder="cm"
            className="w-24 rounded border border-slate-300 px-3 py-2 text-sm"
          />
        </label>
        <button
          type="submit"
          disabled={isCreating}
          className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
        >
          Add attribute
        </button>
      </form>
      {createError && <p className="mt-2 text-sm text-red-600">{createError}</p>}

      <ul className="mt-3 space-y-2">
        {attributesQuery.data?.map((a) => (
          <li key={a.id} className="rounded border border-slate-200 p-2">
            <span className="text-sm font-medium text-slate-900">{a.name}</span>{" "}
            <span className="text-xs text-slate-500">
              ({a.code}, {a.data_type}
              {a.unit ? `, ${a.unit}` : ""})
            </span>
            {a.is_variant_defining && (
              <span className="ml-1 rounded-full bg-indigo-100 px-2 py-0.5 text-xs text-indigo-700">variant axis</span>
            )}
            {(a.data_type === "select" || a.data_type === "multi_select") && (
              <div className="mt-1">
                <div className="flex flex-wrap gap-1">
                  {a.options?.map((o) => (
                    <span key={o.id} className="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-700">
                      {o.value}
                    </span>
                  ))}
                </div>
                <AttributeOptionAdder attributeId={a.id} />
              </div>
            )}
          </li>
        ))}
      </ul>

      <h3 className="mt-6 text-sm font-medium text-slate-900">Assign attribute to a category</h3>
      <div className="mt-2 flex flex-wrap gap-3">
        <select
          value={ruleMainId}
          onChange={(e) => {
            setRuleMainId(e.target.value);
            setRuleMidId("");
            setRuleSubId("");
          }}
          className="rounded border border-slate-300 px-3 py-2 text-sm"
        >
          <option value="">— Select category —</option>
          {mains.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        {ruleMainId && (
          <select
            value={ruleMidId}
            onChange={(e) => {
              setRuleMidId(e.target.value);
              setRuleSubId("");
            }}
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          >
            <option value="">— (this level) —</option>
            {ruleMidOptions.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        )}
        {ruleMidId && ruleSubOptions.length > 0 && (
          <select
            value={ruleSubId}
            onChange={(e) => setRuleSubId(e.target.value)}
            className="rounded border border-slate-300 px-3 py-2 text-sm"
          >
            <option value="">— (this level) —</option>
            {ruleSubOptions.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        )}
      </div>

      {targetCategoryId && (
        <form onSubmit={handleSetRule} className="mt-3 flex flex-wrap items-end gap-3">
          <label className="flex flex-col gap-1 text-sm text-slate-700">
            Attribute
            <select
              value={ruleAttributeId}
              onChange={(e) => setRuleAttributeId(e.target.value)}
              required
              className="rounded border border-slate-300 px-3 py-2 text-sm"
            >
              <option value="" disabled>
                Select…
              </option>
              {attributesQuery.data?.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </label>
          <label className="flex items-center gap-2 text-sm text-slate-700">
            <input type="checkbox" checked={ruleRequired} onChange={(e) => setRuleRequired(e.target.checked)} />
            Required
          </label>
          <label className="flex items-center gap-2 text-sm text-slate-700">
            <input type="checkbox" checked={ruleExcluded} onChange={(e) => setRuleExcluded(e.target.checked)} />
            Exclude (override off)
          </label>
          <label className="flex flex-col gap-1 text-sm text-slate-700">
            Position
            <input
              type="number"
              value={rulePosition}
              onChange={(e) => setRulePosition(Number(e.target.value))}
              className="w-20 rounded border border-slate-300 px-3 py-2 text-sm"
            />
          </label>
          <button
            type="submit"
            disabled={isSettingRule}
            className="rounded bg-slate-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          >
            Save rule
          </button>
        </form>
      )}
      {ruleError && <p className="mt-2 text-sm text-red-600">{ruleError}</p>}

      {targetCategoryId && (
        <div className="mt-3">
          <p className="text-xs font-medium text-slate-500">Effective template for this category:</p>
          <ul className="mt-1 flex flex-wrap gap-2">
            {templateQuery.data?.attributes.map((f) => (
              <li key={f.attribute_id} className="rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-700">
                {f.name}
                {f.required ? " (required)" : ""}
                {f.is_variant_defining ? " (variant axis)" : ""}
              </li>
            ))}
            {templateQuery.data && templateQuery.data.attributes.length === 0 && (
              <li className="text-xs text-slate-500">No attributes apply to this category yet.</li>
            )}
          </ul>
        </div>
      )}
    </div>
  );
}

// AttributeOptionAdder is a small inline form for adding one option value to
// a select/multi_select attribute, embedded in each attribute's row above.
function AttributeOptionAdder({ attributeId }: { attributeId: string }) {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [value, setValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await callWithAuth((token) => api.addAttributeOption(token, attributeId, value));
      setValue("");
      await queryClient.invalidateQueries({ queryKey: ["attributes-admin"] });
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : "Could not add option.");
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="mt-1 flex items-center gap-2">
      <input
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="New option value"
        required
        className="rounded border border-slate-300 px-2 py-1 text-xs"
      />
      <button
        type="submit"
        disabled={isSubmitting}
        className="rounded bg-slate-900 px-2 py-1 text-xs font-medium text-white disabled:opacity-50"
      >
        Add option
      </button>
      {error && <span className="text-xs text-red-600">{error}</span>}
    </form>
  );
}
