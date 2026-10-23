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
import { useQuery } from '@tanstack/react-query'

import { quotePurchase } from '../api'
import {
  paymentInfoQueryOptions,
  paymentMethodsQueryOptions,
  paymentQueryKeys,
} from '../lib/wallet-queries'
import type { PaymentPurpose, PurchaseRequest } from '../types'

// ============================================================================
// Payment Queries
// ============================================================================

/** Site currency, top-up presets and the compliance state. */
export function usePaymentInfo() {
  return useQuery(paymentInfoQueryOptions)
}

/** The methods a purpose can be paid with, unavailable ones included. */
export function usePaymentMethods(purpose: PaymentPurpose, enabled = true) {
  return useQuery({ ...paymentMethodsQueryOptions(purpose), enabled })
}

/**
 * The price of a purchase; idle while the request is null (incomplete).
 * A failed quote is shown next to the price instead of as a toast.
 */
export function usePaymentQuote(request: PurchaseRequest | null) {
  return useQuery({
    queryKey: paymentQueryKeys.quote(request),
    queryFn: () => quotePurchase(request as PurchaseRequest),
    enabled: request !== null,
    retry: false,
    meta: { errorToast: false },
  })
}
