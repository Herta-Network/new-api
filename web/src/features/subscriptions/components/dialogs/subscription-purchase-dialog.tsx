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
import { Crown, CalendarClock, Loader2, Package } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { GroupBadge } from '@/components/group-badge'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { PaymentMethodPicker } from '@/features/wallet/components/payment-method-picker'
import { PaymentQuoteSummary } from '@/features/wallet/components/payment-quote-summary'
import {
  usePaymentMethods,
  usePaymentQuote,
} from '@/features/wallet/hooks/use-payment-queries'
import {
  describeAmountRanges,
  takesAmount,
} from '@/features/wallet/lib/payment'
import type {
  PaymentMethodOption,
  PurchaseRequest,
} from '@/features/wallet/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatMinorAmount } from '@/lib/currency'
import { formatQuota } from '@/lib/format'

import { formatDuration, formatResetPeriod } from '../../lib'
import type { PlanRecord } from '../../types'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  plan: PlanRecord | null
  /** Site currency the plan price is in */
  currency: string
  purchaseLimit?: number
  purchaseCount?: number
  userQuota?: number
  /** Create the order and start its checkout; false when that failed */
  onPurchase: (request: PurchaseRequest) => Promise<boolean>
  purchasing: boolean
}

export function SubscriptionPurchaseDialog(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [methodId, setMethodId] = useState<number>()

  const plan = props.plan?.plan
  const price = Math.round(Number(plan?.price_amount || 0) * 100)
  const free = price === 0
  const methods = usePaymentMethods(
    'subscription',
    props.open && !!plan && !free
  )
  const allowBalancePay = plan?.allow_balance_pay !== false
  const balanceBlocked = (method: PaymentMethodOption) => {
    if (method.balance && !allowBalancePay) {
      return t('This plan does not allow balance redemption')
    }
    if (!takesAmount(method, price)) {
      return t('Takes {{amounts}}', {
        amounts: describeAmountRanges(
          method.amount_ranges,
          props.currency,
          locale,
          t
        ),
      })
    }
    return undefined
  }
  const choosable = (methods.data ?? []).filter(
    (method) => method.available && !balanceBlocked(method)
  )
  const selectedMethod =
    choosable.find((method) => method.id === methodId) ?? choosable[0]
  const limitReached =
    (props.purchaseLimit || 0) > 0 &&
    (props.purchaseCount || 0) >= (props.purchaseLimit || 0)

  const planId = plan?.id
  const selectedMethodId = selectedMethod?.id
  const request = useMemo<PurchaseRequest | null>(() => {
    if (!props.open || !planId || limitReached) return null
    if (free) {
      return { purpose: 'subscription', plan_id: planId, payment_option_id: 0 }
    }
    if (selectedMethodId === undefined) return null
    return {
      purpose: 'subscription',
      plan_id: planId,
      payment_option_id: selectedMethodId,
    }
  }, [free, limitReached, planId, props.open, selectedMethodId])
  const quote = usePaymentQuote(request)

  if (!plan) return null

  const payWithBalance = selectedMethod?.balance === true
  const userQuota = Math.max(0, Number(props.userQuota || 0))
  const insufficientBalance =
    payWithBalance && !!quote.data && quote.data.quota_amount > userQuota
  const totalAmount = Number(plan.total_amount || 0)
  const quoteReady = !!request && !!quote.data && !quote.isFetching

  const handlePay = async () => {
    if (!request) return
    if (await props.onPurchase(request)) {
      props.onOpenChange(false)
    }
  }

  let payLabel = t('Pay')
  if (free) {
    payLabel = t('Subscribe for free')
  } else if (quoteReady && quote.data) {
    payLabel = t('Pay {{amount}}', {
      amount: formatMinorAmount(quote.data.amount, quote.data.currency, locale),
    })
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={
        <>
          <Crown className='h-5 w-5' />
          {t('Purchase Subscription')}
        </>
      }
      contentClassName='max-sm:w-[calc(100vw-1.5rem)] sm:max-w-lg'
      titleClassName='flex items-center gap-2'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <div className='space-y-3 sm:space-y-4'>
        <div className='bg-muted/50 space-y-2.5 rounded-lg border p-3 sm:space-y-3 sm:p-4'>
          <div className='flex justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Plan Name')}
            </span>
            <span className='max-w-[200px] truncate text-sm font-medium'>
              {plan.title}
            </span>
          </div>
          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Validity Period')}
            </span>
            <span className='flex items-center gap-1 text-sm'>
              <CalendarClock className='h-3.5 w-3.5' />
              {formatDuration(plan, t)}
            </span>
          </div>
          {formatResetPeriod(plan, t) !== t('No Reset') && (
            <div className='flex justify-between'>
              <span className='text-muted-foreground text-sm'>
                {t('Reset Period')}
              </span>
              <span className='text-sm'>{formatResetPeriod(plan, t)}</span>
            </div>
          )}
          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('Plan Quota')}
            </span>
            <span className='flex items-center gap-1 text-sm'>
              <Package className='h-3.5 w-3.5' />
              {totalAmount > 0 ? formatQuota(totalAmount) : t('Unlimited')}
            </span>
          </div>
          {plan.upgrade_group && (
            <div className='flex items-center justify-between'>
              <span className='text-muted-foreground text-sm'>
                {t('Upgrade Group')}
              </span>
              <GroupBadge group={plan.upgrade_group} />
            </div>
          )}
          <Separator />
          <div className='flex items-center justify-between'>
            <span className='text-sm font-medium'>{t('Plan Price')}</span>
            <span className='text-primary text-lg font-bold'>
              {formatMinorAmount(price, props.currency, locale)}
            </span>
          </div>
        </div>

        {limitReached && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t('Purchase limit reached')} ({props.purchaseCount}/
              {props.purchaseLimit})
            </AlertDescription>
          </Alert>
        )}

        {!free && (
          <div className='space-y-2'>
            <p className='text-muted-foreground text-xs'>
              {t('Select payment method')}
            </p>
            <PaymentMethodPicker
              methods={methods.data ?? []}
              selectedId={selectedMethod?.id}
              onSelect={(method) => setMethodId(method.id)}
              loading={methods.isLoading}
              disabled={props.purchasing || limitReached}
              getDisabledReason={balanceBlocked}
            />
            {!methods.isLoading && (methods.data ?? []).length === 0 && (
              <Alert>
                <AlertDescription>
                  {t(
                    'No payment methods available. Please contact administrator.'
                  )}
                </AlertDescription>
              </Alert>
            )}
          </div>
        )}

        {request && (
          <PaymentQuoteSummary
            quote={quoteReady ? quote.data : undefined}
            loading={!quoteReady}
            error={quote.error}
            balance={payWithBalance}
          />
        )}

        {payWithBalance && (
          <div className='flex items-center justify-between gap-2 text-xs'>
            <span className='text-muted-foreground'>
              {t('Available balance')}
            </span>
            <span>{formatQuota(userQuota)}</span>
          </div>
        )}
        {insufficientBalance && (
          <Alert variant='destructive'>
            <AlertDescription>{t('Insufficient balance')}</AlertDescription>
          </Alert>
        )}

        <Button
          className='w-full'
          onClick={handlePay}
          disabled={
            !quoteReady ||
            insufficientBalance ||
            limitReached ||
            props.purchasing
          }
        >
          {props.purchasing && <Loader2 className='h-4 w-4 animate-spin' />}
          {payLabel}
        </Button>
      </div>
    </Dialog>
  )
}
