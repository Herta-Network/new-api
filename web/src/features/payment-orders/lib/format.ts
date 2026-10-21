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
import type { TFunction } from 'i18next'

import { formatNumber } from '@/lib/format'

const GATEWAY_NAMES: Record<string, string> = {
  epay: 'EPay',
  stripe: 'Stripe',
  cryptomus: 'Cryptomus',
}

/**
 * An order amount, counted in hundredths of `currency`, as "12.34 USD".
 * `locale` must already be an Intl locale (see `toIntlLocale`).
 */
export function formatOrderAmount(
  amount: number,
  currency: string,
  locale: string | undefined
) {
  return `${formatNumber(amount / 100, locale)} ${currency}`
}

/** A platform's product name; the wallet balance is translated. */
export function paymentPlatformLabel(platform: string, t: TFunction) {
  if (platform === 'balance') return t('Balance')
  return GATEWAY_NAMES[platform] ?? platform
}
