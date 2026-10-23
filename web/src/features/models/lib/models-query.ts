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

import type { ServerPricingMode } from '@/features/model-pricing/api'
import type {
  TableUrlState,
  TableUrlStateConfig,
} from '@/hooks/use-table-url-state'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getModels, getVendors, searchModels } from '../api'
import { DEFAULT_PAGE_SIZE } from '../constants'
import type { ModelSquareState } from '../types'
import { modelsQueryKeys, vendorsQueryKeys } from './query-keys'

/**
 * How the model metadata table keeps its page and filters in the URL. Only
 * an administrator who may edit pricing can filter by pricing mode.
 */
export function getModelsTableUrlConfig(
  isMobile: boolean,
  canPrice: boolean
): TableUrlStateConfig {
  return {
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'square_state', searchKey: 'square_state', type: 'array' },
      { columnId: 'vendor_id', searchKey: 'vendor', type: 'array' },
      { columnId: 'sync_official', searchKey: 'sync', type: 'array' },
      ...(canPrice
        ? [
            {
              columnId: 'billing_mode',
              searchKey: 'pricing',
              type: 'array' as const,
            },
          ]
        : []),
    ],
  }
}

/** Every vendor, for the table's vendor filter and the model form. */
export const vendorsListQueryOptions = queryOptions({
  queryKey: vendorsQueryKeys.list(),
  queryFn: async () =>
    requireServerSuccess(await getVendors({ page_size: 1000 })),
  // Vendors change only through the vendor and model forms, which invalidate
  // `vendorsQueryKeys.all`.
  staleTime: 5 * 60 * 1000,
})

function getFilterValues<T extends string>(
  table: TableUrlState,
  columnId: string
): T[] {
  return (
    (table.columnFilters.find((filter) => filter.id === columnId)?.value as
      | T[]
      | undefined) ?? []
  )
}

/** The first value of an array filter, unless it is the "all" choice. */
function getSelectedFilter(
  table: TableUrlState,
  columnId: string
): string | undefined {
  const values = getFilterValues(table, columnId)
  return values.length > 0 && !values.includes('all') ? values[0] : undefined
}

/**
 * The server-side filters of the metadata table. A vendor picked from the
 * vendors section wins over the vendor filter. Any filter switches the
 * request to the search API.
 */
export function getModelsListFilters(
  table: TableUrlState,
  selectedVendor: string | null
) {
  const filters = {
    keyword: table.globalFilter,
    vendor: selectedVendor || getSelectedFilter(table, 'vendor_id'),
    status: getSelectedFilter(table, 'status'),
    square_state: getFilterValues<ModelSquareState>(table, 'square_state')[0],
    pricing_mode: getFilterValues<ServerPricingMode>(table, 'billing_mode')[0],
    sync_official: getSelectedFilter(table, 'sync_official'),
  }
  const searching = Boolean(
    filters.keyword?.trim() ||
    filters.vendor ||
    filters.status ||
    filters.square_state ||
    filters.pricing_mode ||
    filters.sync_official
  )
  return { filters, searching }
}

/** The page of models the metadata table shows. */
export function modelsListQueryOptions(
  table: TableUrlState,
  selectedVendor: string | null
) {
  const { filters, searching } = getModelsListFilters(table, selectedVendor)
  const page = {
    include_channel_models: true,
    p: table.pagination.pageIndex + 1,
    page_size: table.pagination.pageSize,
  }
  return queryOptions({
    queryKey: modelsQueryKeys.list({ ...page, ...filters }),
    queryFn: async () =>
      requireServerSuccess(
        searching
          ? await searchModels({ ...page, ...filters })
          : await getModels(page)
      ),
  })
}
