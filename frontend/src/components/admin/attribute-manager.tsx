"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

const ATTRIBUTE_DATA_TYPES: api.AttributeDataType[] = [
  "text",
  "number",
  "boolean",
  "select",
  "multi_select",
];

// AttributeManager lets admin build the Attribute Management System's data:
// define attributes (+ options for select/multi_select ones), then assign a
// versioned rule to a category node at any of its 3 levels. A rule set on a
// main-category flows down to its children unless a deeper node overrides
// it (see the resolved-template preview below the rule form, which calls
// the same endpoint the vendor's add-product form renders itself from).
export function AttributeManager() {
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
    <div className="flex flex-col gap-4">
      <SectionHeader title="Attributes" />

      <Card>
        <CardHeader>
          <CardTitle>Attributes</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <form onSubmit={handleCreateAttribute} className="flex flex-wrap items-end gap-3">
            <Label className="flex flex-col items-start gap-1.5 text-sm">
              Code
              <Input value={code} onChange={(e) => setCode(e.target.value)} placeholder="mau_sac" required className="w-32" />
            </Label>
            <Label className="flex flex-col items-start gap-1.5 text-sm">
              Name
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Màu sắc" required className="w-40" />
            </Label>
            <Label className="flex flex-col items-start gap-1.5 text-sm">
              Data type
              <Select
                value={dataType}
                onValueChange={(v) => {
                  const next = v as api.AttributeDataType;
                  setDataType(next);
                  if (next !== "select") setIsVariantDefining(false);
                }}
              >
                <SelectTrigger className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ATTRIBUTE_DATA_TYPES.map((t) => (
                    <SelectItem key={t} value={t}>
                      {t}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Label>
            <Label className={dataType === "select" ? "text-sm" : "text-sm text-muted-foreground"}>
              <Checkbox
                checked={isVariantDefining}
                disabled={dataType !== "select"}
                onCheckedChange={(c) => setIsVariantDefining(c === true)}
              />
              Use for variants
            </Label>
            <Label className="flex flex-col items-start gap-1.5 text-sm">
              Unit (optional)
              <Input value={unit} onChange={(e) => setUnit(e.target.value)} placeholder="cm" className="w-24" />
            </Label>
            <Button type="submit" disabled={isCreating}>
              {isCreating ? "Adding…" : "Add attribute"}
            </Button>
          </form>
          {createError && <p className="text-sm text-destructive">{createError}</p>}
          {attributesQuery.error && (
            <p className="text-sm text-destructive">
              Could not load attributes:{" "}
              {attributesQuery.error instanceof api.ApiError ? attributesQuery.error.message : "unknown error"}
            </p>
          )}

          <Separator />

          {attributesQuery.data?.length === 0 ? (
            <p className="text-sm text-muted-foreground">No attributes yet.</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {attributesQuery.data?.map((a) => (
                <li key={a.id} className="rounded-lg border p-3">
                  <span className="text-sm font-medium">{a.name}</span>{" "}
                  <span className="text-xs text-muted-foreground">
                    ({a.code}, {a.data_type}
                    {a.unit ? `, ${a.unit}` : ""})
                  </span>
                  {a.is_variant_defining && (
                    <Badge variant="info" className="ml-1">
                      variant axis
                    </Badge>
                  )}
                  {(a.data_type === "select" || a.data_type === "multi_select") && (
                    <div className="mt-1">
                      <div className="flex flex-wrap gap-1">
                        {a.options?.map((o) => (
                          <Badge key={o.id} variant="secondary">
                            {o.value}
                          </Badge>
                        ))}
                      </div>
                      <AttributeOptionAdder attributeId={a.id} />
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Assign attribute to a category</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {categoriesQuery.error && (
            <p className="text-sm text-destructive">Could not load categories.</p>
          )}
          <div className="flex flex-wrap gap-3">
            <Select
              value={ruleMainId || "__none__"}
              onValueChange={(v) => {
                setRuleMainId(v === "__none__" ? "" : v);
                setRuleMidId("");
                setRuleSubId("");
              }}
            >
              <SelectTrigger className="w-56">
                <SelectValue placeholder="Select category…" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__none__">— Select category —</SelectItem>
                {mains.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {ruleMainId && (
              <Select
                value={ruleMidId || "__this__"}
                onValueChange={(v) => {
                  setRuleMidId(v === "__this__" ? "" : v);
                  setRuleSubId("");
                }}
              >
                <SelectTrigger className="w-56">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__this__">— (this level) —</SelectItem>
                  {ruleMidOptions.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            {ruleMidId && ruleSubOptions.length > 0 && (
              <Select value={ruleSubId || "__this__"} onValueChange={(v) => setRuleSubId(v === "__this__" ? "" : v)}>
                <SelectTrigger className="w-56">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__this__">— (this level) —</SelectItem>
                  {ruleSubOptions.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          {targetCategoryId && (
            <form onSubmit={handleSetRule} className="flex flex-wrap items-end gap-3">
              <Label className="flex flex-col items-start gap-1.5 text-sm">
                Attribute
                <Select value={ruleAttributeId} onValueChange={setRuleAttributeId}>
                  <SelectTrigger className="w-48">
                    <SelectValue placeholder="Select…" />
                  </SelectTrigger>
                  <SelectContent>
                    {attributesQuery.data?.map((a) => (
                      <SelectItem key={a.id} value={a.id}>
                        {a.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Label>
              <Label className="text-sm">
                <Checkbox checked={ruleRequired} onCheckedChange={(c) => setRuleRequired(c === true)} />
                Required
              </Label>
              <Label className="text-sm">
                <Checkbox checked={ruleExcluded} onCheckedChange={(c) => setRuleExcluded(c === true)} />
                Exclude (override off)
              </Label>
              <Label className="flex flex-col items-start gap-1.5 text-sm">
                Position
                <Input
                  type="number"
                  value={rulePosition}
                  onChange={(e) => setRulePosition(Number(e.target.value))}
                  className="w-20"
                />
              </Label>
              <Button type="submit" disabled={isSettingRule || !ruleAttributeId}>
                {isSettingRule ? "Saving…" : "Save rule"}
              </Button>
            </form>
          )}
          {ruleError && <p className="text-sm text-destructive">{ruleError}</p>}

          {targetCategoryId && (
            <div>
              <p className="text-xs font-medium text-muted-foreground">Effective template for this category:</p>
              {templateQuery.data && templateQuery.data.attributes.length === 0 ? (
                <p className="mt-1 text-sm text-muted-foreground">No attributes apply to this category yet.</p>
              ) : (
                <ul className="mt-1 flex flex-wrap gap-2">
                  {templateQuery.data?.attributes.map((f) => (
                    <li key={f.attribute_id}>
                      <Badge variant="outline">
                        {f.name}
                        {f.required ? " (required)" : ""}
                        {f.is_variant_defining ? " (variant axis)" : ""}
                      </Badge>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </CardContent>
      </Card>
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
      <Input
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="New option value"
        required
        className="h-7 w-40 text-xs"
      />
      <Button type="submit" size="xs" disabled={isSubmitting}>
        Add option
      </Button>
      {error && <span className="text-xs text-destructive">{error}</span>}
    </form>
  );
}
