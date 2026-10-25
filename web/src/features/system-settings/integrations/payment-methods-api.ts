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

/** Fee modes of a payment method (entity.PaymentFee*). */
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

/**
 * The wallet balance: a payment method of fixed id that can be disabled but
 * never deleted, collected by a channel of its own that is not edited.
 */
export const BALANCE_PAYMENT_OPTION_ID = -1
export const BALANCE_PAYMENT_METHOD_ID = -1
export const BALANCE_PAYMENT_PLATFORM = 'balance'

/**
 * A payment method buyers choose (entity.PaymentOption): its name, icon and
 * fee; its channels collect it.
 */
export type PaymentOption = {
  id: number
  name: string
  icon: string
  description: string
  sort: number
  enabled: boolean
  fee_mode: number
  /** Basis points of the price. */
  fee_rate_bp: number
  /** Hundredths of the site currency. */
  fee_fixed: number
  created_at: number
  updated_at: number
  /** How many channels collect it */
  methods: number
}

export type PaymentOptionInput = Pick<
  PaymentOption,
  | 'name'
  | 'icon'
  | 'description'
  | 'sort'
  | 'enabled'
  | 'fee_mode'
  | 'fee_rate_bp'
  | 'fee_fixed'
>

/**
 * A payment channel (entity.PaymentMethod) as the administrator sees it,
 * secrets blanked: a gateway account that collects a payment method within
 * an amount range, by priority and weight. Buyers never see it.
 */
export type PaymentMethod = {
  id: number
  name: string
  platform: string
  /** The payment method it collects */
  option_id: number
  enabled: boolean
  /** Hundredths of the site currency; 0 leaves that end open. */
  min_amount: number
  max_amount: number
  /** The lowest priority is tried first. */
  priority: number
  /** Shares the orders of its priority by weight. */
  weight: number
  domain: string
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
  | 'option_id'
  | 'enabled'
  | 'min_amount'
  | 'max_amount'
  | 'priority'
  | 'weight'
  | 'domain'
  | 'config'
>

export const paymentOptionsQueryKey = ['payment-options'] as const
export const paymentMethodsQueryKey = ['payment-methods'] as const
export const paymentPlatformsQueryKey = ['payment-platforms'] as const

/** The editable fields of a payment method. */
export function toPaymentOptionInput(
  option: PaymentOption
): PaymentOptionInput {
  return {
    name: option.name,
    icon: option.icon,
    description: option.description,
    sort: option.sort,
    enabled: option.enabled,
    fee_mode: option.fee_mode,
    fee_rate_bp: option.fee_rate_bp,
    fee_fixed: option.fee_fixed,
  }
}

/** The editable fields of a channel; secret config values stay blank. */
export function toPaymentMethodInput(
  method: PaymentMethod
): PaymentMethodInput {
  return {
    name: method.name,
    platform: method.platform,
    option_id: method.option_id,
    enabled: method.enabled,
    min_amount: method.min_amount,
    max_amount: method.max_amount,
    priority: method.priority,
    weight: method.weight,
    domain: method.domain,
    config: method.config,
  }
}

export function usePaymentOptions() {
  return useQuery({
    queryKey: paymentOptionsQueryKey,
    queryFn: async () => {
      const res = await api.get<ApiResponse<PaymentOption[]>>(
        '/api/payment/settings/options'
      )
      return requireServerSuccess(res.data).data ?? []
    },
  })
}

type SavePaymentOptionRequest = {
  /** Absent for a new payment method. */
  id?: number
  input: PaymentOptionInput
}

/** What buyers are offered changed: their lists and the admin's. */
function invalidatePaymentLists(
  queryClient: ReturnType<typeof useQueryClient>
) {
  void queryClient.invalidateQueries({ queryKey: paymentOptionsQueryKey })
  void queryClient.invalidateQueries({ queryKey: paymentMethodsQueryKey })
  void queryClient.invalidateQueries({ queryKey: paymentQueryKeys.allMethods })
}

export function useSavePaymentOption() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (request: SavePaymentOptionRequest) => {
      const res =
        request.id === undefined
          ? await api.post<ApiResponse<PaymentOption>>(
              '/api/payment/settings/options',
              request.input
            )
          : await api.put<ApiResponse<PaymentOption>>(
              `/api/payment/settings/options/${request.id}`,
              request.input
            )
      return requireServerSuccess(res.data).data
    },
    onSuccess: () => {
      toast.success(i18next.t('Payment method saved'))
      invalidatePaymentLists(queryClient)
    },
  })
}

export function useDeletePaymentOption() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (id: number) => {
      const res = await api.delete<ApiResponse<null>>(
        `/api/payment/settings/options/${id}`
      )
      return requireServerSuccess(res.data)
    },
    onSuccess: () => {
      toast.success(i18next.t('Payment method deleted'))
      invalidatePaymentLists(queryClient)
    },
  })
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
      toast.success(i18next.t('Payment channel saved'))
      invalidatePaymentLists(queryClient)
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
      toast.success(i18next.t('Payment channel deleted'))
      invalidatePaymentLists(queryClient)
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
      invalidatePaymentLists(queryClient)
    },
  })
}
