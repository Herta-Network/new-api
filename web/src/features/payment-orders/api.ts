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
import { api } from '@/lib/api'

import type {
  ApiResponse,
  PageData,
  PaymentInvoice,
  PaymentOrder,
  PaymentOrderFilters,
  PaymentUnmatched,
  PaymentUnmatchedFilters,
} from './types'

export async function listPaymentOrders(filters: PaymentOrderFilters) {
  const res = await api.get<ApiResponse<PageData<PaymentOrder>>>(
    '/api/payment/admin/orders',
    { params: filters }
  )
  return res.data
}

/** The invoices of an order in the order they were issued. */
export async function listPaymentOrderInvoices(orderNo: string) {
  const res = await api.get<ApiResponse<PaymentInvoice[]>>(
    `/api/payment/admin/orders/${encodeURIComponent(orderNo)}/invoices`
  )
  return res.data
}

/** The order actions an administrator can take on an order. */
export type PaymentOrderAction = 'reconcile' | 'close' | 'fulfil' | 'complete'

export async function runPaymentOrderAction(
  orderNo: string,
  action: PaymentOrderAction,
  note?: string
) {
  const res = await api.post<ApiResponse<PaymentOrder>>(
    `/api/payment/admin/orders/${encodeURIComponent(orderNo)}/${action}`,
    action === 'complete' ? { note } : undefined
  )
  return res.data
}

export async function listPaymentUnmatched(filters: PaymentUnmatchedFilters) {
  const res = await api.get<ApiResponse<PageData<PaymentUnmatched>>>(
    '/api/payment/admin/unmatched',
    { params: filters }
  )
  return res.data
}

export async function resolvePaymentUnmatched(id: number, note: string) {
  const res = await api.post<ApiResponse<null>>(
    `/api/payment/admin/unmatched/${id}/resolve`,
    { note }
  )
  return res.data
}
