"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { SectionHeader } from "@/components/section-header";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";

const CATEGORY_LEVEL_LABELS = ["", "Level 1 (main category)", "Level 2 (category)", "Level 3 (sub-category)"];

export function CategoryManager() {
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
    <div className="flex flex-col gap-4">
      <SectionHeader title="Categories" />

      <Card>
        <CardContent className="flex flex-col gap-3">
          <div className="flex flex-wrap gap-3">
            <Select
              value={mainId || "__root__"}
              onValueChange={(v) => {
                setMainId(v === "__root__" ? "" : v);
                setMidId("");
              }}
            >
              <SelectTrigger className="w-56">
                <SelectValue placeholder="New level 1 category…" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__root__">— New level 1 category —</SelectItem>
                {mains.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {mainId && (
              <Select
                value={midId || "__this__"}
                onValueChange={(v) => setMidId(v === "__this__" ? "" : v)}
              >
                <SelectTrigger className="w-56">
                  <SelectValue placeholder="New level 2 category…" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__this__">— New level 2 category —</SelectItem>
                  {midOptions.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>
          <p className="text-xs text-muted-foreground">Will create: {CATEGORY_LEVEL_LABELS[levelToCreate]}</p>

          <form onSubmit={handleSubmit} className="flex gap-3">
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="New category name"
              required
              className="max-w-xs"
            />
            <Button type="submit" disabled={isSubmitting}>
              {isSubmitting ? "Adding…" : "Add"}
            </Button>
          </form>
          {error && <p className="text-sm text-destructive">{error}</p>}
          {categoriesQuery.error && (
            <p className="text-sm text-destructive">Could not load categories.</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardContent>
          {mains.length === 0 ? (
            <p className="text-sm text-muted-foreground">No categories yet.</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {mains.map((main) => (
                <li key={main.id}>
                  <span className="text-sm font-medium">{main.name}</span>
                  <ul className="mt-1 ml-4 flex flex-col gap-1">
                    {childrenOf(main.id).map((mid) => (
                      <li key={mid.id}>
                        <span className="text-sm">{mid.name}</span>
                        <ul className="mt-1 ml-4 flex flex-col gap-1">
                          {childrenOf(mid.id).map((sub) => (
                            <li key={sub.id} className="text-xs text-muted-foreground">
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
          )}
        </CardContent>
      </Card>
    </div>
  );
}
