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
import { queryOptions, type QueryClient } from '@tanstack/react-query'

import {
  getPublicPlans,
  getSelfSubscriptionFull,
} from '@/features/subscriptions/api'
import { getSelf } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  getAffiliateCode,
  getPaymentInfo,
  getPaymentMethods,
  getRedemptionInfo,
} from '../api'
import type { PaymentPurpose, PurchaseRequest, UserWalletData } from '../types'

// The wallet page's queries. Kept apart from the hooks and the `lib` barrel so
// the route loader can prefetch them without loading the page's components.

export const paymentQueryKeys = {
  all: ['payment'] as const,
  info: ['payment', 'info'] as const,
  allMethods: ['payment', 'methods'] as const,
  methods: (purpose: PaymentPurpose) =>
    ['payment', 'methods', purpose] as const,
  quote: (request: PurchaseRequest | null) =>
    ['payment', 'quote', request] as const,
  orders: ['payment', 'orders'] as const,
  order: (orderNo: string) => ['payment', 'order', orderNo] as const,
}

export const subscriptionQueryKeys = {
  publicPlans: ['subscription', 'plans', 'public'] as const,
  self: ['subscription', 'self'] as const,
}

/**
 * Payment settings, top-up settings and plans change only when an
 * administrator edits them, and those edits invalidate these queries.
 */
const SITE_SETTINGS_STALE_TIME = 5 * 60 * 1000

/** The signed-in user's balance and affiliate totals. */
export const walletUserQueryOptions = queryOptions({
  queryKey: ['wallet', 'user'],
  queryFn: async () => {
    const response = await getSelf()
    return response.success && response.data
      ? (response.data as UserWalletData)
      : null
  },
  // The balance card shows its own empty state when this fails.
  meta: { errorToast: false },
})

export const affiliateCodeQueryOptions = queryOptions({
  queryKey: ['wallet', 'affiliate-code'],
  queryFn: async () =>
    requireServerSuccess(await getAffiliateCode()).data ?? '',
  // A user's affiliate code is created once and never changes.
  staleTime: Infinity,
})

/** Whether codes can be redeemed and the link to buy them. */
export const redemptionInfoQueryOptions = queryOptions({
  queryKey: ['redemption', 'info'],
  queryFn: getRedemptionInfo,
  staleTime: SITE_SETTINGS_STALE_TIME,
})

/** Site currency, top-up presets and the compliance state. */
export const paymentInfoQueryOptions = queryOptions({
  queryKey: paymentQueryKeys.info,
  queryFn: getPaymentInfo,
  staleTime: SITE_SETTINGS_STALE_TIME,
})

/** The methods a purpose can be paid with, unavailable ones included. */
export function paymentMethodsQueryOptions(purpose: PaymentPurpose) {
  return queryOptions({
    queryKey: paymentQueryKeys.methods(purpose),
    queryFn: () => getPaymentMethods(purpose),
    staleTime: SITE_SETTINGS_STALE_TIME,
  })
}

export const subscriptionPlansQueryOptions = queryOptions({
  queryKey: subscriptionQueryKeys.publicPlans,
  queryFn: async () => requireServerSuccess(await getPublicPlans()).data ?? [],
  staleTime: SITE_SETTINGS_STALE_TIME,
})

/** The user's own subscriptions, which a purchase or a refund changes. */
export const selfSubscriptionQueryOptions = queryOptions({
  queryKey: subscriptionQueryKeys.self,
  queryFn: async () =>
    requireServerSuccess(await getSelfSubscriptionFull()).data ?? null,
})

/**
 * Refresh what the wallet builds from the payment settings: prices, presets
 * and quotes, the methods each purpose offers, top-up settings and plans.
 * Called after an administrator saves payment settings or confirms the
 * compliance terms, which gate all of them.
 */
export function invalidatePaymentSettingQueries(queryClient: QueryClient) {
  void queryClient.invalidateQueries({ queryKey: paymentQueryKeys.all })
  void queryClient.invalidateQueries({
    queryKey: redemptionInfoQueryOptions.queryKey,
  })
  void queryClient.invalidateQueries({
    queryKey: subscriptionQueryKeys.publicPlans,
  })
}
