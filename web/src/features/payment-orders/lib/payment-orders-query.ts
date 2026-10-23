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

import type {
  TableUrlState,
  TableUrlStateConfig,
} from '@/hooks/use-table-url-state'
import { requireServerSuccess } from '@/lib/server-error-message'

import { listPaymentOrders, listPaymentUnmatched } from '../api'

export const paymentOrdersQueryKey = ['payment-orders'] as const
export const paymentUnmatchedQueryKey = ['payment-unmatched'] as const

/** How the orders table keeps its page and filters in the URL. */
export const PAYMENT_ORDERS_TABLE_URL_CONFIG: TableUrlStateConfig = {
  pagination: { defaultPage: 1, defaultPageSize: 20 },
  globalFilter: { enabled: true, key: 'filter' },
  columnFilters: [
    { columnId: 'status', searchKey: 'status', type: 'array' },
    { columnId: 'purpose', searchKey: 'purpose', type: 'array' },
    { columnId: 'platform', searchKey: 'platform', type: 'array' },
    { columnId: 'user_id', searchKey: 'user', type: 'string' },
  ],
}

/** How the unmatched payments table keeps its page and filter in the URL. */
export const PAYMENT_UNMATCHED_TABLE_URL_CONFIG: TableUrlStateConfig = {
  pagination: {
    pageKey: 'uPage',
    pageSizeKey: 'uPageSize',
    defaultPage: 1,
    defaultPageSize: 20,
  },
  globalFilter: { enabled: false },
  columnFilters: [
    { columnId: 'resolved_at', searchKey: 'uState', type: 'array' },
  ],
}

function getFilterValue(table: TableUrlState, columnId: string): unknown {
  return table.columnFilters.find((filter) => filter.id === columnId)?.value
}

function getFirstFilter(
  table: TableUrlState,
  columnId: string
): string | undefined {
  return (getFilterValue(table, columnId) as string[] | undefined)?.[0]
}

/** The page of payment orders the orders table shows. */
export function paymentOrdersListQueryOptions(table: TableUrlState) {
  const userId = (getFilterValue(table, 'user_id') as string | undefined) ?? ''
  const filters = {
    p: table.pagination.pageIndex + 1,
    page_size: table.pagination.pageSize,
    order_no: table.globalFilter?.trim() || undefined,
    user_id: userId.trim() || undefined,
    status: getFirstFilter(table, 'status'),
    purpose: getFirstFilter(table, 'purpose'),
    platform: getFirstFilter(table, 'platform'),
  }
  return queryOptions({
    queryKey: [...paymentOrdersQueryKey, filters],
    queryFn: async () =>
      requireServerSuccess(await listPaymentOrders(filters)).data,
  })
}

/** The page of unmatched payments the unmatched table shows. */
export function paymentUnmatchedListQueryOptions(table: TableUrlState) {
  const filters = {
    p: table.pagination.pageIndex + 1,
    page_size: table.pagination.pageSize,
    resolved: getFirstFilter(table, 'resolved_at'),
  }
  return queryOptions({
    queryKey: [...paymentUnmatchedQueryKey, filters],
    queryFn: async () =>
      requireServerSuccess(await listPaymentUnmatched(filters)).data,
  })
}
