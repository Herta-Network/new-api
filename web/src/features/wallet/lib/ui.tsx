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
import { Bitcoin, CreditCard, QrCode, Wallet } from 'lucide-react'
import type { ReactNode } from 'react'

import { IconStripe } from '@/assets/brand-icons'
import { ReactIconByName } from '@/components/react-icon-by-name'

import { PAYMENT_PLATFORMS, STRIPE_ICON_COLOR } from '../constants'

// ============================================================================
// UI Helper Functions
// ============================================================================

/**
 * Resolves a backend-provided image URL to https only. Rejects http:,
 * data:, blob:, file:, relative paths, and URLs with userinfo, which are unsafe
 * or ambiguous in <img src/>.
 */
function normalizeHttpIconUrl(raw: string | undefined | null): string | null {
  if (!raw) return null
  const s = raw.trim()
  if (!s) return null
  if (!/^https:\/\//i.test(s)) return null

  let url: URL
  try {
    url = new URL(s)
  } catch {
    return null
  }
  if (url.protocol !== 'https:') {
    return null
  }
  if (url.username || url.password) {
    return null
  }
  return url.toString()
}

/**
 * Get payment method icon component
 *
 * When icon is provided, render a safe https image URL or resolve it as a
 * react-icons component name. Invalid configured icons intentionally render
 * nothing instead of falling back to the platform icon.
 */
export function getPaymentIcon(
  platform: string | undefined,
  className: string = 'h-4 w-4',
  icon?: string,
  altName?: string
): ReactNode {
  const iconValue = icon?.trim()
  const safeIconUrl = normalizeHttpIconUrl(iconValue)
  if (safeIconUrl) {
    return (
      <img
        src={safeIconUrl}
        alt={altName || platform || 'payment'}
        className={className}
        style={{ objectFit: 'contain' }}
        loading='lazy'
        decoding='async'
        referrerPolicy='no-referrer'
      />
    )
  }
  if (iconValue) {
    return (
      <ReactIconByName
        name={iconValue}
        className={className}
        title={altName || platform || iconValue}
      />
    )
  }

  switch (platform) {
    case PAYMENT_PLATFORMS.STRIPE:
      return (
        <IconStripe
          className={className}
          style={{ color: STRIPE_ICON_COLOR }}
        />
      )
    case PAYMENT_PLATFORMS.EPAY:
      return <QrCode className={className} aria-hidden='true' />
    case PAYMENT_PLATFORMS.CRYPTOMUS:
      return <Bitcoin className={className} aria-hidden='true' />
    case PAYMENT_PLATFORMS.BALANCE:
      return <Wallet className={className} aria-hidden='true' />
    default:
      return <CreditCard className={className} aria-hidden='true' />
  }
}
