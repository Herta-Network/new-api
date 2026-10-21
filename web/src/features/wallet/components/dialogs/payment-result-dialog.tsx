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
  CircleAlert,
  CircleCheck,
  CircleX,
  Clock,
  Loader2,
  type LucideIcon,
} from 'lucide-react'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatMinorAmount } from '@/lib/currency'
import { formatQuota } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import { usePaymentOrderResult } from '../../hooks/use-payment-order-result'
import { isOrderPayable } from '../../lib/billing'
import { PAYMENT_ORDER_STATUS, type PaymentOrder } from '../../types'

interface PaymentResultDialogProps {
  /** The order to follow; the dialog is open while it is set */
  orderNo: string | undefined
  onClose: () => void
  /** The order was fulfilled (quota credited or subscription created) */
  onSettled: () => void
  /** Pay a pending order again */
  onResume?: (orderNo: string) => void
  resuming?: boolean
}

interface OutcomeView {
  icon: LucideIcon
  tone: string
  /** i18n keys */
  title: string
  description: string
}

const CLOSE_REASONS: Record<string, string> = {
  expired: 'The order expired before it was paid.',
  user: 'You cancelled this order.',
  admin: 'An administrator closed this order.',
  payment_failed: 'The payment failed.',
}

function getOutcome(order: PaymentOrder): OutcomeView {
  switch (order.status) {
    case PAYMENT_ORDER_STATUS.FINISHED:
      return {
        icon: CircleCheck,
        tone: 'text-success',
        title: 'Payment successful',
        description:
          order.purpose === 'topup'
            ? 'The quota has been added to your balance.'
            : 'Your subscription is active.',
      }
    case PAYMENT_ORDER_STATUS.PAID:
      if (order.fulfil_error) {
        return {
          icon: CircleAlert,
          tone: 'text-warning',
          title: 'Payment received',
          description:
            'The payment arrived but the purchase could not be delivered. An administrator will review it; contact support with the order number if it takes long.',
        }
      }
      return {
        icon: Loader2,
        tone: 'text-info animate-spin',
        title: 'Payment received',
        description: 'Your purchase is being delivered.',
      }
    case PAYMENT_ORDER_STATUS.CLOSED:
      return {
        icon: CircleX,
        tone: 'text-muted-foreground',
        title: 'Order closed',
        description:
          CLOSE_REASONS[order.close_reason] ??
          'This order was closed without a payment.',
      }
    default:
      return {
        icon: Clock,
        tone: 'text-warning',
        title: 'Waiting for payment',
        description:
          'This page updates by itself once the payment provider confirms the payment.',
      }
  }
}

/**
 * Follows an order after checkout, or after the buyer returns from the
 * payment page, until it is fulfilled or closed.
 */
export function PaymentResultDialog(props: PaymentResultDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const result = usePaymentOrderResult(props.orderNo)
  const order = result.order
  const finished = order?.status === PAYMENT_ORDER_STATUS.FINISHED

  // Refresh what the purchase changed once per fulfilled order.
  const settledOrderRef = useRef<string | null>(null)
  const onSettled = props.onSettled
  useEffect(() => {
    if (!finished || !order || settledOrderRef.current === order.order_no) {
      return
    }
    settledOrderRef.current = order.order_no
    onSettled()
  }, [finished, onSettled, order])

  const outcome = order ? getOutcome(order) : null
  const pending = order?.status === PAYMENT_ORDER_STATUS.PENDING
  const payable = order ? isOrderPayable(order, Date.now() / 1000) : false
  const chargedElsewhere =
    !!order?.payment_currency && order.payment_currency !== order.currency

  return (
    <Dialog
      open={!!props.orderNo}
      onOpenChange={(open) => !open && props.onClose()}
      title={t('Payment status')}
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-md'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          {pending && (
            <Button
              variant='outline'
              onClick={result.checkNow}
              disabled={result.syncing}
            >
              {result.syncing && <Loader2 className='h-4 w-4 animate-spin' />}
              {t('Check again')}
            </Button>
          )}
          {pending && payable && props.onResume && order && (
            <Button
              variant='outline'
              onClick={() => props.onResume?.(order.order_no)}
              disabled={props.resuming}
            >
              {t('Continue payment')}
            </Button>
          )}
          <Button onClick={props.onClose}>
            {finished ? t('Done') : t('Close')}
          </Button>
        </>
      }
    >
      {result.loading && (
        <LoadingState message={t('Checking payment status...')} />
      )}
      {!result.loading && !order && (
        <Alert variant='destructive'>
          <AlertDescription>
            {getServerErrorMessage(result.error, t('Order not found'))}
          </AlertDescription>
        </Alert>
      )}
      {order && outcome && (
        <>
          <div className='flex flex-col items-center gap-2 text-center'>
            <outcome.icon
              className={cn('h-10 w-10', outcome.tone)}
              aria-hidden='true'
            />
            <p className='text-base font-semibold' role='status'>
              {t(outcome.title)}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t(outcome.description)}
            </p>
          </div>
          <dl className='bg-muted/30 space-y-2 rounded-lg border p-3 text-sm'>
            <div className='flex items-center justify-between gap-2'>
              <dt className='text-muted-foreground'>{t('Order number')}</dt>
              <dd className='flex min-w-0 items-center gap-1'>
                <code className='truncate font-mono text-xs'>
                  {order.order_no}
                </code>
                <CopyButton
                  value={order.order_no}
                  size='sm'
                  className='h-6 w-6 p-0'
                  iconClassName='h-3 w-3'
                />
              </dd>
            </div>
            <div className='flex items-center justify-between gap-2'>
              <dt className='text-muted-foreground'>{t('Item')}</dt>
              <dd className='truncate'>{order.subject}</dd>
            </div>
            <div className='flex items-center justify-between gap-2'>
              <dt className='text-muted-foreground'>{t('Total')}</dt>
              <dd className='font-semibold'>
                {formatMinorAmount(order.amount, order.currency, locale)}
              </dd>
            </div>
            {chargedElsewhere && (
              <div className='flex items-center justify-between gap-2'>
                <dt className='text-muted-foreground'>{t('Charged as')}</dt>
                <dd>
                  {formatMinorAmount(
                    order.payment_amount,
                    order.payment_currency,
                    locale
                  )}
                </dd>
              </div>
            )}
            {order.purpose === 'topup' && (
              <div className='flex items-center justify-between gap-2'>
                <dt className='text-muted-foreground'>{t('You receive')}</dt>
                <dd>{formatQuota(order.quota_amount)}</dd>
              </div>
            )}
          </dl>
        </>
      )}
    </Dialog>
  )
}
