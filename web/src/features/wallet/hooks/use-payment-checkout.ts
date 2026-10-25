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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { handleServerError } from '@/lib/handle-server-error'

import { checkoutPaymentOrder, createPaymentOrder } from '../api'
import { paymentQueryKeys } from '../lib/wallet-queries'
import type {
  CheckoutAction,
  PurchaseRequest,
  StripePaymentMethodType,
} from '../types'

// ============================================================================
// Payment Checkout Hook
// ============================================================================

/** A Stripe PaymentIntent the buyer confirms in the flow of its payment method type. */
export interface StripeSession {
  orderNo: string
  publishableKey: string
  clientSecret: string
  paymentMethodType: StripePaymentMethodType
}

/** A QR code a gateway answered for the buyer to scan. */
export interface QrSession {
  orderNo: string
  qrCode: string
}

/**
 * Create orders and follow their checkout: a URL is opened in this tab, a
 * Stripe intent opens the Stripe dialog, a QR code opens the QR dialog, and
 * an order that needs no payment (balance or free) goes straight to
 * onSettling.
 */
export function usePaymentCheckout(onSettling: (orderNo: string) => void) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [stripeSession, setStripeSession] = useState<StripeSession | null>(null)
  const [qrSession, setQrSession] = useState<QrSession | null>(null)

  const follow = useCallback(
    (checkout: CheckoutAction) => {
      void queryClient.invalidateQueries({ queryKey: paymentQueryKeys.orders })
      switch (checkout.type) {
        case 'url':
          toast.success(t('Redirecting to payment page...'))
          window.location.assign(checkout.url)
          return
        case 'stripe':
          setStripeSession({
            orderNo: checkout.order_no,
            publishableKey: checkout.stripe.publishable_key,
            clientSecret: checkout.stripe.client_secret,
            paymentMethodType: checkout.stripe.payment_method_type,
          })
          return
        case 'qrcode':
          setQrSession({ orderNo: checkout.order_no, qrCode: checkout.qr_code })
          return
        default:
          onSettling(checkout.order_no)
      }
    },
    [onSettling, queryClient, t]
  )

  const { mutateAsync: createOrder, isPending: creating } = useMutation({
    mutationFn: createPaymentOrder,
    onError: (error) => handleServerError(error, t('Payment request failed')),
  })
  const { mutateAsync: checkoutOrder, isPending: resuming } = useMutation({
    mutationFn: checkoutPaymentOrder,
    onError: (error) => handleServerError(error, t('Payment request failed')),
  })

  /** Create an order and start paying it; false when that failed. */
  const startPurchase = useCallback(
    async (request: PurchaseRequest): Promise<boolean> => {
      try {
        const created = await createOrder(request)
        follow(created.checkout)
        return true
      } catch {
        return false
      }
    },
    [createOrder, follow]
  )

  /** Pay a pending order again. */
  const resumeOrder = useCallback(
    async (orderNo: string): Promise<boolean> => {
      try {
        follow(await checkoutOrder(orderNo))
        return true
      } catch {
        return false
      }
    },
    [checkoutOrder, follow]
  )

  return {
    startPurchase,
    resumeOrder,
    starting: creating || resuming,
    stripeSession,
    closeStripe: () => setStripeSession(null),
    qrSession,
    closeQr: () => setQrSession(null),
  }
}
