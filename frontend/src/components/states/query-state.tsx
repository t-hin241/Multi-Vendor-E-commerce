import type { ReactNode } from "react";

import { describeApiError } from "@/lib/errors";

import { EmptyState } from "./empty-state";
import { ErrorState } from "./error-state";
import { LoadingState } from "./loading-state";

type MinimalQueryResult<T> = {
  isPending: boolean;
  error: unknown;
  data: T | undefined;
  refetch?: () => void;
};

// Composes the loading/error/empty/success branches every query-backed page
// in this app needs, instead of each page hand-rolling its own
// isPending/error/data.length===0 JSX. Pass `emptyState` + `isEmpty` only
// when "no data" is a distinct, expected outcome worth its own message
// (e.g. an empty cart) — omit both to skip the empty check entirely.
export function QueryState<T>({
  query,
  loading,
  isEmpty,
  emptyState,
  children,
}: {
  query: MinimalQueryResult<T>;
  loading?: ReactNode;
  isEmpty?: (data: T) => boolean;
  emptyState?: ReactNode;
  children: (data: T) => ReactNode;
}) {
  if (query.isPending) {
    return <>{loading ?? <LoadingState />}</>;
  }
  if (query.error) {
    return <ErrorState message={describeApiError(query.error)} onRetry={query.refetch} />;
  }
  if (query.data === undefined) {
    return <ErrorState message="No data returned." onRetry={query.refetch} />;
  }
  if (isEmpty?.(query.data) && emptyState) {
    return <>{emptyState}</>;
  }
  return <>{children(query.data)}</>;
}

export { EmptyState, ErrorState, LoadingState };
