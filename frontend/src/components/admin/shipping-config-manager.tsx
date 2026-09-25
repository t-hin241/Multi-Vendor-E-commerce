"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { formatMoney } from "@/lib/format";

// ShippingConfigManager lets admin manage carriers, shipping zones (and
// which provinces belong to each) and the fee rule for each carrier/zone
// pair — the automatic shipment-fee calculation at checkout depends on all
// three being configured. Fee rules are insert-only, mirroring
// CommissionRuleManager: setting a new one never edits history, so a
// shipment already created keeps the fee it was quoted.
export function ShippingConfigManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();

  const carriersQuery = useQuery({
    queryKey: ["carriers"],
    queryFn: () => callWithAuth((token) => api.listCarriers(token)),
  });
  const zonesQuery = useQuery({
    queryKey: ["zones"],
    queryFn: () => callWithAuth((token) => api.listZones(token)),
  });
  const feeRulesQuery = useQuery({
    queryKey: ["fee-rules"],
    queryFn: () => callWithAuth((token) => api.listFeeRules(token)),
  });

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

  function carrierNameOf(id: string) {
    return carriers.find((c) => c.id === id)?.name ?? id;
  }
  function zoneNameOf(id: string) {
    return zones.find((z) => z.id === id)?.name ?? id;
  }

  return (
    <div className="flex flex-col gap-4">
      <SectionHeader title="Shipping configuration" />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {(carriersQuery.error || zonesQuery.error || feeRulesQuery.error) && (
        <p className="text-sm text-destructive">Could not load shipping configuration.</p>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Carriers</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <form onSubmit={handleCreateCarrier} className="flex gap-2">
              <Input
                value={carrierName}
                onChange={(e) => setCarrierName(e.target.value)}
                placeholder="Name (e.g. GHN)"
                required
              />
              <Input
                value={carrierCode}
                onChange={(e) => setCarrierCode(e.target.value)}
                placeholder="Code"
                required
                className="w-28"
              />
              <Button type="submit" size="sm">
                Add
              </Button>
            </form>
            {carriers.length === 0 ? (
              <p className="text-sm text-muted-foreground">No carriers yet.</p>
            ) : (
              <ul className="flex flex-col gap-1 text-sm">
                {carriers.map((c) => (
                  <li key={c.id}>
                    {c.name} ({c.code}) {c.is_active ? "" : "— inactive"}
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Zones</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <form onSubmit={handleCreateZone} className="flex gap-2">
              <Input
                value={zoneName}
                onChange={(e) => setZoneName(e.target.value)}
                placeholder="Name (e.g. Miền Bắc)"
                required
              />
              <Input
                value={zoneCode}
                onChange={(e) => setZoneCode(e.target.value)}
                placeholder="Code"
                required
                className="w-28"
              />
              <Button type="submit" size="sm">
                Add
              </Button>
            </form>
            {zones.length === 0 ? (
              <p className="text-sm text-muted-foreground">No zones yet.</p>
            ) : (
              <ul className="flex flex-col gap-1 text-sm">
                {zones.map((z) => (
                  <li key={z.id}>
                    {z.name} ({z.code})
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Map a province to a zone</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleAddProvince} className="flex flex-wrap gap-2">
            <Select value={provinceZoneId} onValueChange={setProvinceZoneId}>
              <SelectTrigger className="w-48">
                <SelectValue placeholder="Zone…" />
              </SelectTrigger>
              <SelectContent>
                {zones.map((z) => (
                  <SelectItem key={z.id} value={z.id}>
                    {z.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input
              value={provinceCode}
              onChange={(e) => setProvinceCode(e.target.value)}
              placeholder="Province code (e.g. HN)"
              required
              className="w-56"
            />
            <Button type="submit" size="sm" disabled={!provinceZoneId}>
              Map
            </Button>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Fee rules</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <form onSubmit={handleSetFeeRule} className="flex flex-wrap items-end gap-3">
            <Select value={ruleCarrierId} onValueChange={setRuleCarrierId}>
              <SelectTrigger className="w-40">
                <SelectValue placeholder="Carrier…" />
              </SelectTrigger>
              <SelectContent>
                {carriers.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={ruleZoneId} onValueChange={setRuleZoneId}>
              <SelectTrigger className="w-40">
                <SelectValue placeholder="Zone…" />
              </SelectTrigger>
              <SelectContent>
                {zones.map((z) => (
                  <SelectItem key={z.id} value={z.id}>
                    {z.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Label className="flex flex-col items-start gap-1.5 text-xs">
              Base fee (VND)
              <Input value={baseFee} onChange={(e) => setBaseFee(e.target.value)} required className="w-28" />
            </Label>
            <Label className="flex flex-col items-start gap-1.5 text-xs">
              Free weight (kg)
              <Input value={freeWeightKg} onChange={(e) => setFreeWeightKg(e.target.value)} className="w-24" />
            </Label>
            <Label className="flex flex-col items-start gap-1.5 text-xs">
              Extra fee / kg
              <Input value={extraFeePerKg} onChange={(e) => setExtraFeePerKg(e.target.value)} className="w-28" />
            </Label>
            <Button type="submit" size="sm">
              Set current rule
            </Button>
          </form>

          {feeRules.length === 0 ? (
            <p className="text-sm text-muted-foreground">No fee rules yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Carrier</TableHead>
                  <TableHead>Zone</TableHead>
                  <TableHead>Base fee</TableHead>
                  <TableHead>Free up to</TableHead>
                  <TableHead>Extra / kg</TableHead>
                  <TableHead>Version</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {feeRules.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell>{carrierNameOf(r.carrier_id)}</TableCell>
                    <TableCell>{zoneNameOf(r.zone_id)}</TableCell>
                    <TableCell>{formatMoney(r.base_fee_amount)}</TableCell>
                    <TableCell>{(r.free_weight_grams / 1000).toFixed(1)}kg</TableCell>
                    <TableCell>{formatMoney(r.extra_fee_per_kg)}</TableCell>
                    <TableCell className="text-muted-foreground">v{r.version}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
