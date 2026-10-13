"use client";

import { useCallback, useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import * as api from "@/lib/api-client";
import { useAuth } from "@/lib/auth-context";
import { restoreSelection, selectedCartView } from "@/lib/cart-selection";

const eventName = "shopee-cart-selection";
const memory = new Map<string, string>();
function subscribe(listener: () => void) {
  window.addEventListener("storage", listener);
  window.addEventListener(eventName, listener);
  return () => {
    window.removeEventListener("storage", listener);
    window.removeEventListener(eventName, listener);
  };
}

export function useCartSelection(cart?: api.Cart) {
  const { user, callWithAuth } = useAuth();
  const capabilities = useQuery({
    queryKey: ["checkout-capabilities", user?.id],
    queryFn: () => callWithAuth(api.checkoutCapabilities),
    enabled: user?.role === "buyer" && cart?.selective_checkout_enabled === true,
    staleTime: 0,
    retry: false,
  });
  const scope = user && cart?.cart_id ? `shopee.cart-selection.${user.id}.${cart.cart_id}` : "";
  const read = useCallback(() => {
    if (!scope) return null;
    try {
      return localStorage.getItem(scope) ?? memory.get(scope) ?? null;
    } catch {
      return memory.get(scope) ?? null;
    }
  }, [scope]);
  const raw = useSyncExternalStore(subscribe, read, () => null);
  const active = Boolean(
    scope && cart?.selective_checkout_enabled && capabilities.data?.selective_checkout_enabled,
  );
  const ids = active && cart ? restoreSelection(raw, cart) : undefined;
  const setSelected = (next: string[]) => {
    if (!scope) return;
    const value = JSON.stringify([...new Set(next)].sort());
    memory.set(scope, value);
    try {
      localStorage.setItem(scope, value);
    } catch {
      /* In-memory selection still works. */
    }
    window.dispatchEvent(new Event(eventName));
  };
  const toggle = (lineIDs: string[], checked: boolean) => {
    const current = new Set(ids ?? []);
    for (const id of lineIDs) {
      if (checked) current.add(id);
      else current.delete(id);
    }
    setSelected([...current]);
  };
  return {
    active,
    ids,
    toggle,
    // A Cart that advertises selection must wait for the Order handshake.
    pending: cart?.selective_checkout_enabled === true && capabilities.isPending,
    view: cart ? selectedCartView(cart, ids) : undefined,
  };
}
