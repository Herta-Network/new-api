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
  CardCvcElement,
  CardExpiryElement,
  CardNumberElement,
  useElements,
  useStripe,
} from '@stripe/react-stripe-js'
import type { StripeElementStyle } from '@stripe/stripe-js'
import { Loader2, LockKeyhole } from 'lucide-react'
import { useState, type FormEvent, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldError,
  FieldLabel,
  FieldTitle,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useTheme } from '@/context/theme-provider'
import { cn } from '@/lib/utils'

type CardFieldKey = 'name' | 'number' | 'expiry' | 'cvc'

interface StripeCardFormProps {
  clientSecret: string
  onCancel: () => void
  onConfirmed: () => void
}

/**
 * Collects a card payment as ppanel does: the cardholder name, then Stripe's
 * card number, expiry and CVC fields, which keep the card details in
 * Stripe's iframes. confirmCardPayment runs 3D Secure when the bank asks.
 */
export function StripeCardForm(props: StripeCardFormProps) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const stripe = useStripe()
  const elements = useElements()
  const [cardholder, setCardholder] = useState('')
  const [fieldErrors, setFieldErrors] = useState<
    Partial<Record<CardFieldKey, string>>
  >({})
  const [ready, setReady] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const dark = resolvedTheme === 'dark'
  // Stripe's fields live in iframes and cannot read the page's CSS
  // variables; 16px keeps phones from zooming in on focus.
  const style: StripeElementStyle = {
    base: {
      fontSize: '16px',
      color: dark ? '#fafafa' : '#0a0a0a',
      iconColor: dark ? '#a1a1aa' : '#71717a',
      '::placeholder': { color: dark ? '#71717a' : '#a1a1aa' },
    },
    invalid: { color: '#ef4444', iconColor: '#ef4444' },
  }
  const setFieldError = (field: CardFieldKey, message: string | undefined) =>
    setFieldErrors((current) => ({ ...current, [field]: message }))

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault()
    if (!stripe || !elements) return
    const name = cardholder.trim()
    if (!name) {
      setFieldError('name', t('Enter the cardholder name'))
      return
    }
    const card = elements.getElement(CardNumberElement)
    if (!card) return
    setSubmitting(true)
    setError(null)
    try {
      const result = await stripe.confirmCardPayment(props.clientSecret, {
        payment_method: { card, billing_details: { name } },
      })
      if (result.error) {
        setError(result.error.message || t('Payment failed'))
        return
      }
      props.onConfirmed()
    } catch {
      setError(t('Payment failed'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className='space-y-4' noValidate>
      <Field data-invalid={!!fieldErrors.name}>
        <FieldLabel htmlFor='stripe-cardholder'>
          {t('Cardholder name')}
        </FieldLabel>
        <Input
          id='stripe-cardholder'
          autoComplete='cc-name'
          className='md:text-base'
          value={cardholder}
          aria-invalid={!!fieldErrors.name}
          onChange={(event) => {
            setCardholder(event.target.value)
            setFieldError('name', undefined)
          }}
        />
        <FieldError>{fieldErrors.name}</FieldError>
      </Field>
      <StripeCardField label={t('Card number')} error={fieldErrors.number}>
        <CardNumberElement
          options={{ style, showIcon: true }}
          onReady={() => setReady(true)}
          onChange={(event) => setFieldError('number', event.error?.message)}
          onLoadError={() =>
            setError(
              t('The payment form could not be loaded. Try again later.')
            )
          }
        />
      </StripeCardField>
      <div className='grid grid-cols-2 gap-4'>
        <StripeCardField label={t('Expiry date')} error={fieldErrors.expiry}>
          <CardExpiryElement
            options={{ style }}
            onChange={(event) => setFieldError('expiry', event.error?.message)}
          />
        </StripeCardField>
        <StripeCardField label={t('CVC')} error={fieldErrors.cvc}>
          <CardCvcElement
            options={{ style }}
            onChange={(event) => setFieldError('cvc', event.error?.message)}
          />
        </StripeCardField>
      </div>
      {error && (
        <Alert variant='destructive'>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <p className='text-muted-foreground flex items-center gap-1.5 text-xs'>
        <LockKeyhole className='h-3.5 w-3.5' aria-hidden='true' />
        {t('Secured by the payment provider')}
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

/**
 * A labelled Stripe card field framed like the project's Input: the field
 * itself is Stripe's iframe, so the frame carries focus and error states.
 */
function StripeCardField(props: {
  label: string
  error: string | undefined
  children: ReactNode
}) {
  return (
    <Field data-invalid={!!props.error}>
      <FieldTitle>{props.label}</FieldTitle>
      <div
        className={cn(
          'border-input focus-within:border-ring focus-within:ring-ring/50 dark:bg-input/30 flex h-8 w-full items-center rounded-lg border px-2.5 transition-colors focus-within:ring-3 focus-within:ring-inset',
          props.error &&
            'border-destructive ring-destructive/20 dark:border-destructive/50 dark:ring-destructive/40 ring-3 ring-inset'
        )}
      >
        <div className='w-full'>{props.children}</div>
      </div>
      <FieldError>{props.error}</FieldError>
    </Field>
  )
}
