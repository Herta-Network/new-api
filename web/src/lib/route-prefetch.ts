/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type {
  EnsureQueryDataOptions,
  QueryClient,
  QueryKey,
} from '@tanstack/react-query'

/**
 * Start a page's data request from its route loader.
 *
 * The router runs loaders when a link is hovered or focused
 * (`defaultPreload: 'intent'`) and again on navigation, so the request is in
 * flight or cached by the time the page mounts and its `useQuery` reads the
 * same options. Fresh data is reused without a request and stale data is
 * refreshed in the background, so hovering again does not refetch.
 *
 * The loader never waits for the response, so navigation is not held up. A
 * failure is silent (see the `prefetch` query meta): a hover never toasts or
 * redirects, and the page's own query retries on mount and reports it.
 */
export function prefetchRouteData<
  TQueryFnData,
  TError,
  TData,
  TQueryKey extends QueryKey,
>(
  queryClient: QueryClient,
  options: EnsureQueryDataOptions<TQueryFnData, TError, TData, TQueryKey>
): void {
  queryClient
    .ensureQueryData({
      ...options,
      revalidateIfStale: true,
      meta: { ...options.meta, prefetch: true },
    })
    .catch(() => undefined)
}
