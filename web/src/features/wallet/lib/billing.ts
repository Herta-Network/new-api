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
import type { StatusBadgeProps } from '@/components/status-badge'

import { PAYMENT_PLATFORM_LABELS } from '../constants'
import { PAYMENT_ORDER_STATUS, type PaymentOrder } from '../types'

// ============================================================================
// Billing Utility Functions
// ============================================================================

interface StatusConfig {
  variant: StatusBadgeProps['variant']
  /** i18n key */
  label: string
}

const STATUS_CONFIG: Record<number, StatusConfig> = {
  [PAYMENT_ORDER_STATUS.PENDING]: {
    variant: 'warning',
    label: 'Awaiting payment',
  },
  [PAYMENT_ORDER_STATUS.PAID]: { variant: 'info', label: 'Paid' },
  [PAYMENT_ORDER_STATUS.CLOSED]: { variant: 'neutral', label: 'Closed' },
  [PAYMENT_ORDER_STATUS.FINISHED]: { variant: 'success', label: 'Completed' },
}

/**
 * Status badge of a payment order
 */
export function getOrderStatusConfig(status: number): StatusConfig {
  return STATUS_CONFIG[status] ?? STATUS_CONFIG[PAYMENT_ORDER_STATUS.PENDING]
}

/**
 * Whether the buyer can still pay an order (pending and not expired)
 */
export function isOrderPayable(order: PaymentOrder, nowSeconds: number) {
  return (
    order.status === PAYMENT_ORDER_STATUS.PENDING &&
    order.platform !== 'balance' &&
    order.expires_at > nowSeconds
  )
}

/**
 * Platform name (i18n key) of an order
 */
export function getPlatformLabel(platform: string): string {
  return PAYMENT_PLATFORM_LABELS[platform] ?? platform
}
