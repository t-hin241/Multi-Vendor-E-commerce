import { ApiError } from "@/lib/api-client";

// Centralizes the `err instanceof ApiError ? err.message : "..."` branch
// that used to be hand-copied into every catch block across the app.
export function describeApiError(err: unknown, fallback = "Something went wrong."): string {
  return err instanceof ApiError ? err.message : fallback;
}
