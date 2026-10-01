import { checkoutAttempt, type CheckoutAttempt } from "@/lib/order-workflow";

// A checkout attempt (its Idempotency-Key) survives a reload, a lost
// response and other tabs: placing the same cart version, address and
// total again reuses the key, so Order returns the order it already
// created instead of a second one. The record holds a random key and the
// attempt's inputs only (no token or personal data), is scoped to the
// signed-in user and expires after a day.

const PREFIX = "shopee.checkout-attempt.";
const TTL_MS = 24 * 60 * 60 * 1000;

export type StoredAttempt = CheckoutAttempt & { createdAt: number };

// Storage is optional (private mode, disabled): everything still works,
// the key just lives as long as the page.
type Store = Pick<Storage, "getItem" | "setItem" | "removeItem">;

function store(): Store | null {
  try {
    return typeof window === "undefined" ? null : window.localStorage;
  } catch {
    return null;
  }
}

export function loadAttempt(userId: string, now = Date.now(), s = store()): StoredAttempt | null {
  if (!s) return null;
  try {
    const raw = s.getItem(PREFIX + userId);
    if (!raw) return null;
    const value = JSON.parse(raw) as Partial<StoredAttempt>;
    if (
      typeof value.key !== "string" ||
      typeof value.fingerprint !== "string" ||
      typeof value.createdAt !== "number" ||
      now - value.createdAt > TTL_MS
    ) {
      s.removeItem(PREFIX + userId);
      return null;
    }
    return value as StoredAttempt;
  } catch {
    return null;
  }
}

// nextAttempt returns the attempt to send for this input: the stored one
// when the input is the same, otherwise a new one (which replaces it).
export function nextAttempt(
  userId: string,
  input: { addressId: string; cartVersion: number; expectedTotalAmount: number },
  now = Date.now(),
  s = store(),
): StoredAttempt {
  const previous = loadAttempt(userId, now, s);
  const attempt = checkoutAttempt(previous, input);
  const stored: StoredAttempt =
    previous && attempt === previous ? previous : { ...attempt, createdAt: now };
  try {
    s?.setItem(PREFIX + userId, JSON.stringify(stored));
  } catch {
    /* Storage full or disabled: the key still covers retries on this page. */
  }
  return stored;
}

// clearAttempt forgets the attempt once its order is known.
export function clearAttempt(userId: string, s = store()) {
  try {
    s?.removeItem(PREFIX + userId);
  } catch {
    /* ignore */
  }
}
