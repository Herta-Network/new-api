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
import { formatPricingNumber } from '@/features/system-settings/models/pricing-format'
import type { CurrencyConfig } from '@/stores/system-config-store'

export type PricingCurrency = {
  label: string
  symbol: string
  exchangeRate: number
}

export const USD_PRICING_CURRENCY: PricingCurrency = {
  label: 'USD',
  symbol: '$',
  exchangeRate: 1,
}

/**
 * The site currency prices can be entered in, or null for a USD site. Its
 * rate is NaN while the site currency has no usable rate.
 */
export function getSitePricingCurrency(
  config: CurrencyConfig
): PricingCurrency | null {
  if (!config.siteCurrency || config.siteCurrency === 'USD') return null
  return {
    label: config.siteCurrency,
    symbol: config.siteCurrencySymbol || config.siteCurrency,
    exchangeRate: config.usdRate ?? Number.NaN,
  }
}

export function isValidPricingCurrency(
  currency: PricingCurrency | null
): currency is PricingCurrency {
  return (
    currency !== null &&
    Number.isFinite(currency.exchangeRate) &&
    currency.exchangeRate > 0
  )
}

export function formatPricingAmount(
  value: string | number,
  currency = USD_PRICING_CURRENCY
): string {
  if (value === '') return ''
  const amount = Number(value) * currency.exchangeRate
  if (!Number.isFinite(amount)) return '—'
  return `${currency.symbol}${formatPricingNumber(amount)}`
}
