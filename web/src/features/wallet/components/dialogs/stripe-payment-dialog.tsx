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
import { Elements } from '@stripe/react-stripe-js'
import {
  loadStripe,
  type Stripe,
  type StripeElementLocale,
} from '@stripe/stripe-js'
import { CatchBoundary } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'

import type { StripeSession } from '../../hooks/use-payment-checkout'
import { StripeCardForm } from './stripe-card-form'
import { StripeQrPayment } from './stripe-qr-payment'

/** Stripe.js loads once per publishable key; a failed load resolves to null. */
const stripeLoaders = new Map<string, Promise<Stripe | null>>()

const STRIPE_LOCALES = new Set<string>(['en', 'fr', 'ja', 'ru', 'vi', 'zh-TW'])

interface StripePaymentDialogProps {
  session: StripeSession | null
  onClose: () => void
  /**
   * Stripe accepted the payment without leaving the page, or the order of a
   * shown QR code stopped waiting for it: the order result takes over.
   */
  onConfirmed: (orderNo: string) => void
}

/**
 * Pays a Stripe PaymentIntent in the flow of the one payment method type it
 * collects: card details in Stripe's own card fields, or the Alipay or
 * WeChat Pay QR code Stripe returns.
 */
export function StripePaymentDialog(props: StripePaymentDialogProps) {
  const { t, i18n } = useTranslation()
  const session = props.session
  const [stripe, setStripe] = useState<{
    key: string
    instance: Stripe | null
  } | null>(null)

  useEffect(() => {
    if (!session) return
    const key = session.publishableKey
    let loader = stripeLoaders.get(key)
    if (!loader) {
      loader = loadStripe(key).catch(() => {
        stripeLoaders.delete(key)
        return null
      })
      stripeLoaders.set(key, loader)
    }
    let active = true
    void loader.then((instance) => {
      if (active) setStripe({ key, instance })
    })
    return () => {
      active = false
    }
  }, [session])

  const intlLocale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  let locale: StripeElementLocale = 'auto'
  if (intlLocale === 'zh-CN') {
    locale = 'zh'
  } else if (intlLocale && STRIPE_LOCALES.has(intlLocale)) {
    locale = intlLocale as StripeElementLocale
  }
  // undefined while Stripe.js loads, null when it failed to load.
  const stripeInstance =
    session && stripe?.key === session.publishableKey
      ? stripe.instance
      : undefined
  const isCard = session?.paymentMethodType === 'card'

  return (
    <Dialog
      open={!!session}
      onOpenChange={(open) => !open && props.onClose()}
      title={t('Complete payment')}
      description={
        isCard
          ? t(
              'Your payment details go straight to the payment provider and never reach this site.'
            )
          : t(
              'This page updates by itself once the payment provider confirms the payment.'
            )
      }
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-lg'
      contentHeight='auto'
    >
      {stripeInstance === undefined && (
        <Skeleton className='h-48 w-full rounded-lg' />
      )}
      {stripeInstance === null && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'The payment form could not be loaded. Check your network and try again.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {session && stripeInstance && (
        // Stripe.js throws on input it rejects; keep that inside the dialog
        // instead of the route error page.
        <CatchBoundary
          getResetKey={() => session.clientSecret}
          errorComponent={StripeFormError}
        >
          {isCard ? (
            <Elements
              key={session.clientSecret}
              stripe={stripeInstance}
              options={{ locale }}
            >
              <StripeCardForm
                clientSecret={session.clientSecret}
                onCancel={props.onClose}
                onConfirmed={() => props.onConfirmed(session.orderNo)}
              />
            </Elements>
          ) : (
            <StripeQrPayment
              key={session.clientSecret}
              stripe={stripeInstance}
              session={session}
              onCancel={props.onClose}
              onConfirmed={() => props.onConfirmed(session.orderNo)}
            />
          )}
        </CatchBoundary>
      )}
    </Dialog>
  )
}

function StripeFormError() {
  const { t } = useTranslation()
  return (
    <Alert variant='destructive'>
      <AlertDescription>
        {t('The payment form could not be loaded. Try again later.')}
      </AlertDescription>
    </Alert>
  )
}
