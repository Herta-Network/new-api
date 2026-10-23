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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { useDebounce } from '@/hooks/use-debounce'

import { getPaymentOrder, listPaymentOrders } from '../api'
import { paymentQueryKeys } from '../lib/wallet-queries'
import type { PaymentOrder } from '../types'

// ============================================================================
// Billing History Hook
// ============================================================================

/**
 * The user's payment orders, a page at a time. Searching by order number
 * looks that one order up instead.
 */
export function useBillingHistory(enabled: boolean) {
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [keyword, setKeyword] = useState('')
  const orderNo = useDebounce(keyword.trim())

  const list = useQuery({
    queryKey: [...paymentQueryKeys.orders, page, pageSize],
    queryFn: () => listPaymentOrders(page, pageSize),
    enabled: enabled && !orderNo,
    placeholderData: keepPreviousData,
  })
  const found = useQuery({
    queryKey: paymentQueryKeys.order(orderNo),
    queryFn: () => getPaymentOrder(orderNo),
    enabled: enabled && !!orderNo,
    retry: false,
    meta: { errorToast: false },
  })

  let records: PaymentOrder[] = list.data?.items ?? []
  let total = list.data?.total ?? 0
  let loading = list.isLoading
  if (orderNo) {
    records = found.data ? [found.data] : []
    total = records.length
    loading = found.isLoading
  }

  return {
    records,
    total,
    page,
    pageSize,
    keyword,
    searching: keyword.trim() !== '',
    loading: loading || keyword.trim() !== orderNo,
    handlePageChange: setPage,
    handlePageSizeChange: (size: number) => {
      setPageSize(size)
      setPage(1)
    },
    handleSearch: (value: string) => {
      setKeyword(value)
      setPage(1)
    },
  }
}
