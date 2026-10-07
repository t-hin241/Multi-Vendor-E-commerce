"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";

import { ActionError } from "@/components/admin/action-error";
import { ReasonDialog } from "@/components/admin/confirm-dialogs";
import { SectionHeader } from "@/components/section-header";
import { Badge } from "@/components/ui/badge";
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
import { Textarea } from "@/components/ui/textarea";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import {
  POLICY_KINDS,
  RETURN_SHIPPING_OPTIONS,
  formatEffectiveAt,
  policyVersionHref,
  returnsRuleRefs,
} from "@/lib/policies";

const STATUS_VARIANT: Record<string, "outline" | "warning" | "success" | "secondary"> = {
  draft: "outline",
  preparing: "warning",
  published: "success",
  withdrawn: "secondary",
};

// PolicyManager drafts and publishes marketplace policy versions and
// reviews shop policy proposals. Vendor publishes a version only once Order
// acknowledged every rule it cites; until then it stays "preparing" and
// the previous version remains in force. Published versions never change:
// a correction is a new draft.
export function PolicyManager() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const [kind, setKind] = useState<api.PolicyKind>("returns");
  const policies = useQuery({
    queryKey: ["admin-policies", kind],
    queryFn: () => callWithAuth((token) => api.listAdminPolicies(token, { kind })),
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["admin-policies"] });

  return (
    <div className="flex flex-col gap-6">
      <SectionHeader
        title="Policies"
        subtitle="Versioned marketplace policies. Orders keep the version in force when they were placed."
        action={
          <Link
            href={`/policies/${kind}`}
            target="_blank"
            className="text-sm text-primary underline"
          >
            Public page
          </Link>
        }
      />
      <div className="flex flex-wrap gap-2">
        {POLICY_KINDS.map((k) => (
          <Button
            key={k.kind}
            size="sm"
            variant={kind === k.kind ? "default" : "outline"}
            onClick={() => setKind(k.kind)}
          >
            {k.kind}
          </Button>
        ))}
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_24rem]">
        <div className="flex flex-col gap-2">
          {policies.error && <ActionError error={policies.error} />}
          {policies.data?.length === 0 && (
            <p className="text-sm text-muted-foreground">No version of this policy yet.</p>
          )}
          {policies.data?.map((p) => (
            <PolicyRow key={p.id} policy={p} onChanged={refresh} />
          ))}
        </div>
        <DraftForm kind={kind} onCreated={refresh} />
      </div>

      <ShopProposals />
    </div>
  );
}

function PolicyRow({
  policy: p,
  onChanged,
}: {
  policy: api.MarketplacePolicy;
  onChanged: () => void;
}) {
  const { callWithAuth } = useAuth();
  const open = p.status === "draft" || p.status === "preparing";
  const notReady = Object.entries(p.readiness ?? {}).filter(([, r]) => !r.ready);
  return (
    <Card>
      <CardContent className="flex flex-col gap-2 text-sm">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="font-medium">
            v{p.version} · {p.title}
          </p>
          <Badge variant={STATUS_VARIANT[p.status ?? "draft"]}>{p.status}</Badge>
        </div>
        <p className="text-muted-foreground">
          Effective {formatEffectiveAt(p.effective_at)} (Asia/Saigon) · hash{" "}
          {p.content_hash.slice(0, 12)}
        </p>
        {Object.keys(p.rule_refs).length > 0 && (
          <p className="font-mono text-xs">
            {Object.entries(p.rule_refs)
              .map(([k, v]) => `${k}=${v}`)
              .join(" · ")}
          </p>
        )}
        {p.status === "preparing" && (
          <p className="text-xs text-warning">
            Waiting for rule owners
            {notReady.length > 0 &&
              `: ${notReady.map(([k, r]) => `${k} (${r.reason})`).join("; ")}`}
            . Retried every 30 seconds; the previous version stays in force.
          </p>
        )}
        <p className="line-clamp-2 text-muted-foreground">{p.summary}</p>
        <div className="flex flex-wrap gap-2">
          {p.status === "published" && (
            <Link
              className="text-primary underline"
              href={policyVersionHref(p.kind, p.version)}
              target="_blank"
            >
              View
            </Link>
          )}
          {open && (
            <ReasonDialog
              trigger={
                <Button size="sm">
                  {p.status === "preparing" ? "Retry publication" : "Publish"}
                </Button>
              }
              title={`Publish ${p.kind} v${p.version}?`}
              description="It applies to orders placed from its effective time. It can never be edited afterwards; shops are notified."
              confirmLabel="Publish"
              variant="default"
              onConfirm={async (reason) => {
                await callWithAuth((t) => api.publishPolicy(t, p.id, p.row_version ?? 0, reason));
                onChanged();
              }}
            />
          )}
          {open && (
            <ReasonDialog
              trigger={
                <Button size="sm" variant="outline">
                  Withdraw
                </Button>
              }
              title={`Withdraw ${p.kind} v${p.version}?`}
              confirmLabel="Withdraw"
              onConfirm={async (reason) => {
                await callWithAuth((t) => api.withdrawPolicy(t, p.id, p.row_version ?? 0, reason));
                onChanged();
              }}
            />
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function toLocalInput(d: Date) {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function DraftForm({ kind, onCreated }: { kind: api.PolicyKind; onCreated: () => void }) {
  const { callWithAuth } = useAuth();
  const [title, setTitle] = useState("");
  const [summary, setSummary] = useState("");
  const [content, setContent] = useState("");
  const [contact, setContact] = useState("");
  const [effectiveAt, setEffectiveAt] = useState(() =>
    toLocalInput(new Date(Date.now() + 10 * 60_000)),
  );
  const [windowDays, setWindowDays] = useState(7);
  const [shipping, setShipping] = useState("none");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const valid = title.trim() && summary.trim() && content.trim() && contact.trim() && effectiveAt;

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      await callWithAuth((t) =>
        api.createPolicyDraft(t, {
          kind,
          title,
          summary,
          content,
          contact,
          // datetime-local is the admin's local time; sent as UTC.
          effective_at: new Date(effectiveAt).toISOString(),
          rule_refs: kind === "returns" ? returnsRuleRefs(windowDays, shipping) : {},
        }),
      );
      setTitle("");
      setSummary("");
      setContent("");
      onCreated();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">New {kind} draft</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <div className="flex flex-col gap-1">
          <Label htmlFor="policy-title">Title</Label>
          <Input
            id="policy-title"
            maxLength={200}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="policy-summary">Summary (shown at checkout)</Label>
          <Textarea
            id="policy-summary"
            rows={2}
            maxLength={1000}
            value={summary}
            onChange={(e) => setSummary(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="policy-content">Content (plain text, Vietnamese)</Label>
          <Textarea
            id="policy-content"
            rows={8}
            maxLength={50000}
            value={content}
            onChange={(e) => setContent(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="policy-contact">Contact and opening hours</Label>
          <Textarea
            id="policy-contact"
            rows={2}
            maxLength={1000}
            value={contact}
            onChange={(e) => setContact(e.target.value)}
          />
        </div>
        {kind === "returns" && (
          <div className="flex flex-col gap-2 rounded-md border p-2">
            <p className="text-xs text-muted-foreground">
              Rules Order enforces for orders placed under this version. The text must say the same.
            </p>
            <Label htmlFor="policy-window">Return window (days after delivery)</Label>
            <Input
              id="policy-window"
              type="number"
              min={1}
              max={365}
              value={windowDays}
              onChange={(e) => setWindowDays(Math.floor(Number(e.target.value)) || 0)}
            />
            <Label>Shipping fee on returns</Label>
            <Select value={shipping} onValueChange={setShipping}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {RETURN_SHIPPING_OPTIONS.map((o) => (
                  <SelectItem key={o.value} value={o.value}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        <div className="flex flex-col gap-1">
          <Label htmlFor="policy-effective">Effective from (your local time)</Label>
          <Input
            id="policy-effective"
            type="datetime-local"
            value={effectiveAt}
            onChange={(e) => setEffectiveAt(e.target.value)}
          />
        </div>
        <ActionError error={error} />
        <Button
          disabled={!valid || busy || (kind === "returns" && (windowDays < 1 || windowDays > 365))}
          onClick={submit}
        >
          {busy ? "Saving…" : "Create draft"}
        </Button>
      </CardContent>
    </Card>
  );
}

function ShopProposals() {
  const { callWithAuth } = useAuth();
  const queryClient = useQueryClient();
  const proposals = useQuery({
    queryKey: ["admin-shop-policy-proposals"],
    queryFn: () => callWithAuth((token) => api.listShopPolicyProposals(token)),
  });
  const refresh = () =>
    queryClient.invalidateQueries({ queryKey: ["admin-shop-policy-proposals"] });

  return (
    <section className="flex flex-col gap-2">
      <SectionHeader
        title="Shop policy proposals"
        subtitle="A shop may add to the marketplace policies, never lower them. Approval makes the text public."
      />
      {proposals.error && <ActionError error={proposals.error} />}
      {proposals.data?.length === 0 && (
        <p className="text-sm text-muted-foreground">Nothing to review.</p>
      )}
      {proposals.data?.map((p) => (
        <Card key={p.id}>
          <CardContent className="flex flex-col gap-2 text-sm">
            <p className="text-muted-foreground">
              Shop {p.vendor_id.slice(0, 8)} · v{p.version}
              {p.source === "legacy" && " · imported from the old free-text policy"}
            </p>
            <p className="whitespace-pre-wrap">{p.content}</p>
            <div className="flex flex-wrap gap-2">
              <ReasonDialog
                trigger={<Button size="sm">Approve</Button>}
                title="Approve this shop policy?"
                description="Check it does not contradict the marketplace policies. A note is optional; type '-' if none."
                confirmLabel="Approve"
                variant="default"
                onConfirm={async (reason) => {
                  await callWithAuth((t) =>
                    api.decideShopPolicy(t, p.id, true, reason === "-" ? "" : reason),
                  );
                  refresh();
                }}
              />
              <ReasonDialog
                trigger={
                  <Button size="sm" variant="outline">
                    Reject
                  </Button>
                }
                title="Reject this shop policy?"
                description="The shop sees the reason and can propose again."
                confirmLabel="Reject"
                onConfirm={async (reason) => {
                  await callWithAuth((t) => api.decideShopPolicy(t, p.id, false, reason));
                  refresh();
                }}
              />
            </div>
          </CardContent>
        </Card>
      ))}
    </section>
  );
}
