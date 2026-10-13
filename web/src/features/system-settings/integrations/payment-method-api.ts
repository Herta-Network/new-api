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
import { requireServerSuccess } from '@/lib/server-error-message'

export interface PaymentMethodData {
  id: number
  name: string
  provider: 'epay'
  icon: string
  sort: number
  enabled: boolean
  version_id: number
  has_key?: boolean
  config: {
    endpoint: string
    merchant_id: string
    key: string
    payment_type: string
  }
}

const endpoint = '/api/option/payment-methods'

export async function getPaymentMethods(): Promise<PaymentMethodData[]> {
  const { data } = await api.get<{
    success: boolean
    data: PaymentMethodData[]
  }>(endpoint)
  requireServerSuccess(data)
  return data.data
}

export async function savePaymentMethod(method: PaymentMethodData) {
  const response = method.id
    ? await api.put(`${endpoint}/${method.id}`, method)
    : await api.post(endpoint, method)
  requireServerSuccess(response.data)
}

export async function deletePaymentMethod(id: number) {
  const response = await api.delete(`${endpoint}/${id}`)
  requireServerSuccess(response.data)
}
