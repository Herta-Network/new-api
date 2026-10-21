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
export type ApiResponse<T> = {
  success: boolean
  message?: string
  data: T
}

export type PageData<T> = {
  page: number
  page_size: number
  total: number
  items: T[] | null
}

/** A payment order; amounts count hundredths of their currency. */
export type PaymentOrder = {
  id: number
  order_no: string
  user_id: number
  purpose: string
  plan_id: number
  subject: string
  topup_amount: number
  quota_amount: number
  quota_rate: string
  currency: string
  price: number
  adjustment: number
  fee_amount: number
  amount: number
  payment_method_id: number
  platform: string
  /** What the gateway was asked to collect; empty currency = no checkout. */
  payment_amount: number
  payment_currency: string
  exchange_rate: string
  trade_no: string
  status: number
  fulfil_error: string
  user_subscription_id: number
  close_reason: string
  created_at: number
  expires_at: number
  paid_at: number
  finished_at: number
  closed_at: number
}

export type PaymentOrderFilters = {
  p: number
  page_size: number
  order_no?: string
  user_id?: string
  status?: string
  purpose?: string
  platform?: string
}

/** A gateway payment that could not settle its order. */
export type PaymentUnmatched = {
  id: number
  platform: string
  trade_no: string
  order_no: string
  payment_method_id: number
  user_id: number
  amount: number
  currency: string
  reason: string
  created_at: number
  resolved_at: number
  resolved_by: number
  resolve_note: string
}

export type PaymentUnmatchedFilters = {
  p: number
  page_size: number
  /** 'true' or 'false'; absent lists both. */
  resolved?: string
}
