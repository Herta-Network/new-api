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
import {
  Elements,
  PaymentElement,
  useElements,
  useStripe,
} from '@stripe/react-stripe-js'
import {
  loadStripe,
  type Stripe,
  type StripeElementLocale,
} from '@stripe/stripe-js'
import { CatchBoundary } from '@tanstack/react-router'
import { Loader2, LockKeyhole } from 'lucide-react'
import { useEffect, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useTheme } from '@/context/theme-provider'
import { toIntlLocale } from '@/i18n/languages'

import type { StripeSession } from '../../hooks/use-payment-checkout'

/** Stripe.js loads once per publishable key; a failed load resolves to null. */
const stripeLoaders = new Map<string, Promise<Stripe | null>>()

const STRIPE_LOCALES = new Set<string>(['en', 'fr', 'ja', 'ru', 'vi', 'zh-TW'])

interface StripePaymentDialogProps {
  session: StripeSession | null
  onClose: () => void
  /** Stripe accepted the payment without leaving the page */
  onConfirmed: (orderNo: string) => void
}

/**
 * Collects the payment of a Stripe PaymentIntent in Stripe's Payment Element,
 * so card numbers stay in Stripe's own iframe. Payment methods that need a
 * redirect come back to the wallet with the order number.
 */
export function StripePaymentDialog(props: StripePaymentDialogProps) {
  const { t, i18n } = useTranslation()
  const { resolvedTheme } = useTheme()
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

  return (
    <Dialog
      open={!!session}
      onOpenChange={(open) => !open && props.onClose()}
      title={t('Pay with Stripe')}
      description={t(
        'Your payment details go straight to Stripe and never reach this site.'
      )}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-lg'
      contentHeight='auto'
    >
      {stripeInstance === undefined && (
        <Skeleton className='h-48 w-full rounded-lg' />
      )}
      {stripeInstance === null && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Stripe could not be loaded. Check your network and try again.')}
          </AlertDescription>
        </Alert>
      )}
      {session && stripeInstance && (
        // Stripe.js throws while rendering on a client secret it rejects;
        // keep that inside the dialog instead of the route error page.
        <CatchBoundary
          getResetKey={() => session.clientSecret}
          errorComponent={StripeFormError}
        >
          <Elements
            key={session.clientSecret}
            stripe={stripeInstance}
            options={{
              clientSecret: session.clientSecret,
              locale,
              appearance: {
                theme: resolvedTheme === 'dark' ? 'night' : 'stripe',
              },
            }}
          >
            <StripePaymentForm
              orderNo={session.orderNo}
              onCancel={props.onClose}
              onConfirmed={props.onConfirmed}
            />
          </Elements>
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

interface StripePaymentFormProps {
  orderNo: string
  onCancel: () => void
  onConfirmed: (orderNo: string) => void
}

function StripePaymentForm(props: StripePaymentFormProps) {
  const { t } = useTranslation()
  const stripe = useStripe()
  const elements = useElements()
  const [ready, setReady] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault()
    if (!stripe || !elements) return
    setSubmitting(true)
    setError(null)
    try {
      const result = await stripe.confirmPayment({
        elements,
        confirmParams: {
          return_url: `${window.location.origin}/wallet?payment_order=${encodeURIComponent(props.orderNo)}`,
        },
        redirect: 'if_required',
      })
      if (result.error) {
        setError(result.error.message || t('Payment failed'))
        return
      }
      props.onConfirmed(props.orderNo)
    } catch {
      setError(t('Payment failed'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className='space-y-4'>
      <PaymentElement
        onReady={() => setReady(true)}
        onLoadError={() =>
          setError(t('The payment form could not be loaded. Try again later.'))
        }
      />
      {error && (
        <Alert variant='destructive'>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <p className='text-muted-foreground flex items-center gap-1.5 text-xs'>
        <LockKeyhole className='h-3.5 w-3.5' aria-hidden='true' />
        {t('Secured by Stripe')}
      </p>
      <div className='flex flex-col-reverse gap-2 sm:flex-row sm:justify-end'>
        <Button
          type='button'
          variant='outline'
          onClick={props.onCancel}
          disabled={submitting}
        >
          {t('Cancel')}
        </Button>
        <Button
          type='submit'
          disabled={!stripe || !elements || !ready || submitting}
        >
          {submitting && <Loader2 className='h-4 w-4 animate-spin' />}
          {t('Pay now')}
        </Button>
      </div>
    </form>
  )
}
