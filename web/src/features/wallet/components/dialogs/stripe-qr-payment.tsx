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
import type { PaymentIntentResult, Stripe } from '@stripe/stripe-js'
import { Loader2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import type { StripeSession } from '../../hooks/use-payment-checkout'
import { usePaymentOrderResult } from '../../hooks/use-payment-order-result'
import { PAYMENT_ORDER_STATUS } from '../../types'
import { PaymentQrCode } from './payment-qr-code'

interface StripeQrPaymentProps {
  stripe: Stripe
  session: StripeSession
  onCancel: () => void
  /** The order stopped waiting for its payment */
  onConfirmed: () => void
}

/** Stripe.js does not type Alipay's next action. */
interface AlipayNextAction {
  alipay_handle_redirect?: { url?: string }
}

type QrState =
  | { status: 'confirming' }
  | { status: 'failed'; message: string }
  /** qrValue is drawn as a QR code; imageUrl is Stripe's own QR image */
  | { status: 'shown'; qrValue?: string; imageUrl?: string }

/**
 * Pays an Alipay or WeChat Pay intent as ppanel does: the intent is
 * confirmed without Stripe.js taking over the page, and the QR code of its
 * next action is shown to be scanned. Once it is shown, the order is
 * followed until the payment settles it or it closes. Alipay can also be
 * opened on this device, for buyers on a phone.
 */
export function StripeQrPayment(props: StripeQrPaymentProps) {
  const { t } = useTranslation()
  const session = props.session
  const isAlipay = session.paymentMethodType === 'alipay'
  const [state, setState] = useState<QrState>({ status: 'confirming' })
  // Confirm each intent once, also when development mode runs effects twice.
  const confirmedRef = useRef<string | null>(null)
  const onConfirmed = props.onConfirmed

  useEffect(() => {
    if (confirmedRef.current === session.clientSecret) return
    confirmedRef.current = session.clientSecret
    const notStarted = t('The payment could not be started. Try again later.')
    const confirm = async () => {
      let result: PaymentIntentResult
      try {
        result = isAlipay
          ? await props.stripe.confirmAlipayPayment(
              session.clientSecret,
              {
                return_url: `${window.location.origin}/user/wallet?payment_order=${encodeURIComponent(session.orderNo)}`,
              },
              { handleActions: false }
            )
          : await props.stripe.confirmWechatPayPayment(
              session.clientSecret,
              { payment_method_options: { wechat_pay: { client: 'web' } } },
              { handleActions: false }
            )
      } catch {
        setState({ status: 'failed', message: notStarted })
        return
      }
      if (result.error) {
        setState({
          status: 'failed',
          message: result.error.message || notStarted,
        })
        return
      }
      const intent = result.paymentIntent
      if (intent.status === 'succeeded' || intent.status === 'processing') {
        onConfirmed()
        return
      }
      const qrValue = isAlipay
        ? (intent.next_action as AlipayNextAction | null)
            ?.alipay_handle_redirect?.url
        : undefined
      const imageUrl = isAlipay
        ? undefined
        : intent.next_action?.wechat_pay_display_qr_code?.image_data_url
      if (!qrValue && !imageUrl) {
        setState({ status: 'failed', message: notStarted })
        return
      }
      setState({ status: 'shown', qrValue, imageUrl })
    }
    void confirm()
  }, [isAlipay, onConfirmed, props.stripe, session, t])

  const result = usePaymentOrderResult(
    state.status === 'shown' ? session.orderNo : undefined
  )
  const orderStatus = result.order?.status
  useEffect(() => {
    if (
      orderStatus !== undefined &&
      orderStatus !== PAYMENT_ORDER_STATUS.PENDING
    ) {
      onConfirmed()
    }
  }, [onConfirmed, orderStatus])

  const scanText = isAlipay
    ? t('Scan with Alipay to pay')
    : t('Scan with WeChat to pay')

  return (
    <div className='space-y-4'>
      {state.status === 'confirming' && (
        <div
          role='status'
          className='text-muted-foreground flex h-48 items-center justify-center gap-2 text-sm'
        >
          <Loader2 className='h-4 w-4 animate-spin' aria-hidden='true' />
          {t('Preparing the payment...')}
        </div>
      )}
      {state.status === 'failed' && (
        <Alert variant='destructive'>
          <AlertDescription>{state.message}</AlertDescription>
        </Alert>
      )}
      {state.status === 'shown' && (
        <PaymentQrCode
          value={state.qrValue}
          imageUrl={state.imageUrl}
          label={scanText}
          openUrl={isAlipay ? state.qrValue : undefined}
          openLabel={t('Open Alipay on this device')}
        />
      )}
      <div className='flex justify-end'>
        <Button type='button' variant='outline' onClick={props.onCancel}>
          {t('Close')}
        </Button>
      </div>
    </div>
  )
}
