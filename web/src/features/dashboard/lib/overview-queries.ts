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
import { queryOptions } from '@tanstack/react-query'

import { getApiKeys } from '@/features/keys/api'
import { getUserModels } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { computeTimeRange } from '@/lib/time'

import { getUserQuotaDates } from '../api'

/** The first API keys, to tell whether the setup guide's first step is done. */
export const overviewApiKeysQueryOptions = queryOptions({
  queryKey: ['dashboard', 'overview', 'api-keys'],
  queryFn: async () => {
    const result = requireServerSuccess(await getApiKeys({ p: 1, size: 10 }))
    return result.success ? (result.data?.items ?? []) : []
  },
  staleTime: 60 * 1000,
})

/** The models the user may call, for the example request. */
export const overviewUserModelsQueryOptions = queryOptions({
  queryKey: ['dashboard', 'overview', 'user-models'],
  queryFn: async () => {
    const result = requireServerSuccess(await getUserModels())
    return result.success ? (result.data ?? []) : []
  },
  staleTime: 5 * 60 * 1000,
})

/**
 * Hourly usage over the 24 hours before the request. The window is taken when
 * the request starts and returned with the data, so a cached entry keeps the
 * range its buckets were counted in.
 */
export const overviewUsageTrendQueryOptions = queryOptions({
  queryKey: ['dashboard', 'overview', 'summary-sparklines'],
  queryFn: async () => {
    const range = computeTimeRange(1)
    const result = requireServerSuccess(
      await getUserQuotaDates({ ...range, default_time: 'hour' })
    )
    return { ...range, items: result.data ?? [] }
  },
  staleTime: 60 * 1000,
})
