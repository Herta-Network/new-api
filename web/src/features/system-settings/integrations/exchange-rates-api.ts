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

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { STATUS_QUERY_KEY, STATUS_STORAGE_KEY } from '@/lib/status-query'

type ApiResponse<T> = {
  success: boolean
  message?: string
  data: T
}

/** What the site converts a currency pair for. */
export type ExchangeRateUse = 'quota' | 'epay' | 'channel_balance'

/** A currency pair as the price of one `base` in `quote`. */
export type ExchangeRatePair = {
  base: string
  quote: string
  /** Empty for an override nothing uses any more. */
  uses: ExchangeRateUse[]
  /** The newest stored live rate, null when none was fetched. */
  live: { rate: string; fetched_at: number } | null
  /** The saved override of one base in quote, empty when there is none. */
  override: string
  /** State of the live rate, whether or not an override is set. */
  status: 'live' | 'stale' | 'missing'
}

export type ExchangeRateOverview = {
  currency: string
  pairs: ExchangeRatePair[]
}

/** Stored live rates older than this are not used (rateMaxAge). */
export const LIVE_RATE_MAX_AGE_SECONDS = 72 * 3600

export const exchangeRatesQueryKey = ['payment-exchange-rates'] as const

/** The pairs a site in `currency` converts with and their rates. */
export function useExchangeRates(currency: string) {
  return useQuery({
    queryKey: [...exchangeRatesQueryKey, currency],
    queryFn: async () => {
      const res = await api.get<ApiResponse<ExchangeRateOverview>>(
        '/api/payment/settings/rates',
        { params: { currency } }
      )
      return requireServerSuccess(res.data).data
    },
    enabled: Boolean(currency),
  })
}

/** Fetches the live rates the site needs now. */
export function useRefreshExchangeRates() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      const res = await api.post<ApiResponse<ExchangeRateOverview>>(
        '/api/payment/settings/rates/refresh'
      )
      return requireServerSuccess(res.data).data
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: exchangeRatesQueryKey })
      void queryClient.invalidateQueries({ queryKey: STATUS_QUERY_KEY })
      try {
        window.localStorage.removeItem(STATUS_STORAGE_KEY)
      } catch {
        /* Storage can be unavailable in private mode. */
      }
    },
  })
}
