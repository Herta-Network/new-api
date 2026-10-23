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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'

import { getPaymentOrder, syncPaymentOrder } from '../api'
import {
  ORDER_POLL_INTERVAL_MS,
  ORDER_POLL_WINDOW_MS,
  ORDER_SYNC_SCHEDULE_SECONDS,
} from '../constants'
import { paymentQueryKeys } from '../lib/wallet-queries'
import { PAYMENT_ORDER_STATUS, type PaymentOrder } from '../types'

// ============================================================================
// Payment Order Result Hook
// ============================================================================

/** An order whose outcome is still on its way: unpaid, or paid and being fulfilled. */
function isOrderSettling(order: PaymentOrder | undefined): boolean {
  if (!order) return false
  if (order.status === PAYMENT_ORDER_STATUS.PENDING) return true
  return order.status === PAYMENT_ORDER_STATUS.PAID && !order.fulfil_error
}

/**
 * Follow an order until it settles or closes. The order is re-read every few
 * seconds; the gateway itself is asked only on a short schedule (see
 * ORDER_SYNC_SCHEDULE_SECONDS), or when the buyer asks with checkNow.
 */
export function usePaymentOrderResult(orderNo: string | undefined) {
  const queryClient = useQueryClient()
  const openedAtRef = useRef(0)

  useEffect(() => {
    openedAtRef.current = Date.now()
  }, [orderNo])

  const order = useQuery({
    queryKey: paymentQueryKeys.order(orderNo ?? ''),
    queryFn: () => getPaymentOrder(orderNo as string),
    enabled: !!orderNo,
    retry: false,
    meta: { errorToast: false },
    refetchInterval: (query) =>
      isOrderSettling(query.state.data) &&
      Date.now() - openedAtRef.current < ORDER_POLL_WINDOW_MS
        ? ORDER_POLL_INTERVAL_MS
        : false,
  })

  const { mutate: sync, isPending: syncing } = useMutation({
    mutationFn: syncPaymentOrder,
    meta: { errorToast: false },
    onSuccess: (synced) => {
      queryClient.setQueryData(paymentQueryKeys.order(synced.order_no), synced)
    },
  })

  const pending = order.data?.status === PAYMENT_ORDER_STATUS.PENDING
  useEffect(() => {
    if (!orderNo || !pending) return
    const elapsed = Date.now() - openedAtRef.current
    const timers = ORDER_SYNC_SCHEDULE_SECONDS.map((seconds) =>
      window.setTimeout(
        () => sync(orderNo),
        Math.max(0, seconds * 1000 - elapsed)
      )
    )
    return () => timers.forEach((timer) => window.clearTimeout(timer))
  }, [orderNo, pending, sync])

  return {
    order: order.data,
    loading: order.isLoading,
    error: order.error,
    syncing,
    checkNow: () => {
      if (orderNo) sync(orderNo)
    },
  }
}
