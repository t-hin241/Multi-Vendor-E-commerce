import { ApiError } from "@/lib/api-client";

// describeApiError words a failure for the user. ApiError messages are
// already safe (see safeMessage in api-client): a business refusal keeps
// the server's wording; a lost response or server failure says so with the
// request id, prefixed by what the page was trying to do.
export function describeApiError(err: unknown, fallback = "Đã có lỗi xảy ra."): string {
  if (!(err instanceof ApiError)) return fallback;
  if (err.status === 0 || err.status >= 500) return `${fallback} ${err.message}`;
  return err.message || fallback;
}
