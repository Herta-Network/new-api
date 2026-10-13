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
import { authRequestOptions } from '@/lib/secure-verification'
import { requireServerSuccess } from '@/lib/server-error-message'

export type PaymentEventStatus =
  | 'pending'
  | 'review'
  | 'rejected'
  | 'completed'
  | 'ignored'
  | 'resolved'
export type PaymentReviewAction = 'hold' | 'retry' | 'resolve'
export interface PaymentEvent {
  id: number
  order_no: string
  settlement_order_no: string
  trade_no: string
  amount: number
  currency: string
  status: PaymentEventStatus
  attempts: number
  last_error: string
  created_at: number
  next_attempt_at: number
  revision: number
}
export interface PaymentEventDetails {
  event: PaymentEvent
  amount_decimal: string
  order: {
    user_id: number
    provider: string
    status: string
    renewal_stopped: boolean
  }
  actions: {
    id: number
    actor_id: number
    action: PaymentReviewAction
    reason: string
    created_at: number
  }[]
}
export interface PaymentReviewInput {
  event_id: number
  revision: number
  action: PaymentReviewAction
  reason: string
}

const endpoint = '/api/option/payment-events'

export async function getPaymentEvents(
  params: { p: number; page_size: number; status: string; order_no: string },
  signal?: AbortSignal
) {
  const response = await api.get<{
    success: boolean
    data: { items: PaymentEvent[]; total: number }
  }>(endpoint, { params, signal })
  requireServerSuccess(response.data)
  return response.data.data
}

export async function getPaymentEvent(id: number, signal?: AbortSignal) {
  const response = await api.get<{
    success: boolean
    data: PaymentEventDetails
  }>(`${endpoint}/${id}`, { signal })
  requireServerSuccess(response.data)
  return response.data.data
}

export async function reviewPaymentEvent(
  input: PaymentReviewInput,
  proof: string,
  signal: AbortSignal
) {
  const response = await api.post(
    `${endpoint}/${input.event_id}/review`,
    {
      revision: input.revision,
      action: input.action,
      reason: input.reason,
    },
    { ...authRequestOptions, signal, headers: { 'X-Security-Proof': proof } }
  )
  requireServerSuccess(response.data)
}
