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
import i18next from 'i18next'
import { toast } from 'sonner'

import { paymentQueryKeys } from '@/features/wallet/lib/wallet-queries'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

type ApiResponse<T> = {
  success: boolean
  message?: string
  data: T
}

/** A configuration field of a gateway platform, as the server describes it. */
export type PaymentPlatformField = {
  key: string
  /** English source text; the frontend translates it. */
  label: string
  /** A hint under the field, English source text. */
  description?: string
  /**
   * 'select' takes one of options, 'textarea' is text of several lines;
   * absent for a one-line text field.
   */
  type?: 'select' | 'textarea'
  required: boolean
  /** Never returned by the server; empty input keeps the stored value. */
  secret: boolean
  /** Maintained by the server and not editable. */
  managed: boolean
  /** The values of a select field, in display order. */
  options?: PaymentPlatformFieldOption[]
  /** The field applies only while these fields hold these values. */
  when?: Record<string, string>
}

export type PaymentPlatformFieldOption = {
  value: string
  /** English source text; the frontend translates it. */
  label: string
}

export type PaymentPlatform = {
  platform: string
  name: string
  url: string
  fields: PaymentPlatformField[]
}

export type PaymentPlatformsInfo = {
  platforms: PaymentPlatform[]
  currencies: string[]
  exchange_rate_api_key_configured: boolean
}

/** Fee modes of a payment method (model.PaymentFee*). */
export const PAYMENT_FEE_MODE = {
  NONE: 0,
  RATE: 1,
  FIXED: 2,
  RATE_AND_FIXED: 3,
} as const

export const PAYMENT_FEE_MODE_OPTIONS = [
  { value: PAYMENT_FEE_MODE.NONE, labelKey: 'No fee' },
  { value: PAYMENT_FEE_MODE.RATE, labelKey: 'Percentage' },
  { value: PAYMENT_FEE_MODE.FIXED, labelKey: 'Fixed amount' },
  { value: PAYMENT_FEE_MODE.RATE_AND_FIXED, labelKey: 'Percentage + fixed' },
] as const

/** The wallet balance method: fixed id, can be disabled but never deleted. */
export const BALANCE_PAYMENT_METHOD_ID = -1
export const BALANCE_PAYMENT_PLATFORM = 'balance'

/** A payment method as the administrator sees it, secrets blanked. */
export type PaymentMethod = {
  id: number
  name: string
  platform: string
  icon: string
  description: string
  sort: number
  enabled: boolean
  domain: string
  fee_mode: number
  /** Basis points of the price. */
  fee_rate_bp: number
  /** Hundredths of the site currency. */
  fee_fixed: number
  created_at: number
  updated_at: number
  config: Record<string, unknown>
  secrets_set: Record<string, boolean>
  notify_url: string
  webhook_stale: boolean
}

export type PaymentMethodInput = Pick<
  PaymentMethod,
  | 'name'
  | 'platform'
  | 'icon'
  | 'description'
  | 'sort'
  | 'enabled'
  | 'domain'
  | 'config'
  | 'fee_mode'
  | 'fee_rate_bp'
  | 'fee_fixed'
>

export const paymentMethodsQueryKey = ['payment-methods'] as const
export const paymentPlatformsQueryKey = ['payment-platforms'] as const

/** The editable fields of a method; secret config values stay blank. */
export function toPaymentMethodInput(
  method: PaymentMethod
): PaymentMethodInput {
  return {
    name: method.name,
    platform: method.platform,
    icon: method.icon,
    description: method.description,
    sort: method.sort,
    enabled: method.enabled,
    domain: method.domain,
    config: method.config,
    fee_mode: method.fee_mode,
    fee_rate_bp: method.fee_rate_bp,
    fee_fixed: method.fee_fixed,
  }
}

export function usePaymentPlatforms() {
  return useQuery({
    queryKey: paymentPlatformsQueryKey,
    queryFn: async () => {
      const res = await api.get<ApiResponse<PaymentPlatformsInfo>>(
        '/api/payment/settings/platforms'
      )
      return requireServerSuccess(res.data).data
    },
  })
}

export function usePaymentMethods() {
  return useQuery({
    queryKey: paymentMethodsQueryKey,
    queryFn: async () => {
      const res = await api.get<ApiResponse<PaymentMethod[]>>(
        '/api/payment/settings/methods'
      )
      return requireServerSuccess(res.data).data ?? []
    },
  })
}

type SavePaymentMethodRequest = {
  /** Absent for a new method. */
  id?: number
  input: PaymentMethodInput
}

export function useSavePaymentMethod() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (request: SavePaymentMethodRequest) => {
      const res =
        request.id === undefined
          ? await api.post<ApiResponse<PaymentMethod>>(
              '/api/payment/settings/methods',
              request.input
            )
          : await api.put<ApiResponse<PaymentMethod>>(
              `/api/payment/settings/methods/${request.id}`,
              request.input
            )
      return requireServerSuccess(res.data).data
    },
    onSuccess: () => {
      toast.success(i18next.t('Payment method saved'))
      void queryClient.invalidateQueries({ queryKey: paymentMethodsQueryKey })
      void queryClient.invalidateQueries({
        queryKey: paymentQueryKeys.allMethods,
      })
    },
  })
}

export function useDeletePaymentMethod() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (id: number) => {
      const res = await api.delete<ApiResponse<null>>(
        `/api/payment/settings/methods/${id}`
      )
      return requireServerSuccess(res.data)
    },
    onSuccess: () => {
      toast.success(i18next.t('Payment method deleted'))
      void queryClient.invalidateQueries({ queryKey: paymentMethodsQueryKey })
      void queryClient.invalidateQueries({
        queryKey: paymentQueryKeys.allMethods,
      })
    },
  })
}

export function useSyncPaymentWebhook() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (id: number) => {
      const res = await api.post<ApiResponse<PaymentMethod>>(
        `/api/payment/settings/methods/${id}/webhook/sync`
      )
      return requireServerSuccess(res.data).data
    },
    onSuccess: () => {
      toast.success(i18next.t('Webhook synced'))
      void queryClient.invalidateQueries({ queryKey: paymentMethodsQueryKey })
      void queryClient.invalidateQueries({
        queryKey: paymentQueryKeys.allMethods,
      })
    },
  })
}
