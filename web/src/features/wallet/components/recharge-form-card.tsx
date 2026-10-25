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
import { Gift, ExternalLink, Loader2, Receipt, WalletCards } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { TitledCard } from '@/components/ui/titled-card'
import { useDebounce } from '@/hooks/use-debounce'
import { toIntlLocale } from '@/i18n/languages'
import { formatMinorAmount } from '@/lib/currency'
import { cn } from '@/lib/utils'

import { usePaymentQuote } from '../hooks/use-payment-queries'
import { getDiscountLabel } from '../lib/format'
import {
  describeAmountRanges,
  getAmountDiscount,
  parseMinorAmount,
  takesAmount,
} from '../lib/payment'
import type {
  PaymentInfo,
  PaymentMethodOption,
  PurchaseRequest,
} from '../types'
import { PaymentMethodPicker } from './payment-method-picker'
import { PaymentQuoteSummary } from './payment-quote-summary'

interface RechargeFormCardProps {
  info: PaymentInfo | undefined
  loading?: boolean
  methods: PaymentMethodOption[]
  methodsLoading?: boolean
  onPurchase: (request: PurchaseRequest) => Promise<boolean>
  purchasing: boolean
  redemptionCode: string
  onRedemptionCodeChange: (code: string) => void
  onRedeem: () => void
  redeeming: boolean
  /** Where to buy redemption codes */
  topupLink?: string
  onOpenBilling?: () => void
}

export function RechargeFormCard(props: RechargeFormCardProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [amountInput, setAmountInput] = useState<string | null>(null)
  const [methodId, setMethodId] = useState<number>()

  const info = props.info
  const currency = info?.currency ?? 'USD'
  const minimum = parseMinorAmount(info?.min_topup ?? '') ?? 0
  const presets = info?.amount_options ?? []
  // Until the buyer types, the first preset (or the minimum) is selected.
  const amountText =
    amountInput ??
    (presets.length > 0 ? String(presets[0]) : info?.min_topup) ??
    ''
  const amount = parseMinorAmount(amountText)
  const belowMinimum = amount !== null && amount < minimum
  // Until the amount is valid every method may take it.
  const takes = (method: PaymentMethodOption) =>
    amount === null || belowMinimum || takesAmount(method, amount)
  const availableMethods = props.methods.filter(
    (method) => method.available && takes(method)
  )
  const selectedMethod =
    availableMethods.find((method) => method.id === methodId) ??
    availableMethods[0]
  const noMethodTakesAmount =
    !props.methodsLoading &&
    availableMethods.length === 0 &&
    props.methods.some((method) => method.available)

  const request = useMemo<PurchaseRequest | null>(() => {
    if (!selectedMethod || amount === null || amount <= 0 || belowMinimum) {
      return null
    }
    return {
      purpose: 'topup',
      payment_option_id: selectedMethod.id,
      amount: amount / 100,
    }
  }, [amount, belowMinimum, selectedMethod])
  // Quote once the request stopped changing for a moment.
  const debouncedRequest = useDebounce(request, 400)
  const quoteRequest = debouncedRequest === request ? request : null
  const quote = usePaymentQuote(quoteRequest)
  const quoteSettled =
    quoteRequest !== null && !!quote.data && !quote.isFetching

  const hasMethods = props.methods.length > 0

  if (props.loading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
          <Skeleton className='h-6 w-32' />
          <Skeleton className='mt-2 h-4 w-48' />
        </CardHeader>
        <CardContent className='space-y-4 p-3 sm:space-y-6 sm:p-5'>
          <div className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
            {['first', 'second', 'third', 'fourth'].map((key) => (
              <Skeleton key={key} className='h-14 rounded-lg' />
            ))}
          </div>
          <Skeleton className='h-[42px] w-full' />
          <Skeleton className='h-24 w-full' />
        </CardContent>
      </Card>
    )
  }

  return (
    <TitledCard
      title={t('Add Funds')}
      description={t('Choose an amount and payment method')}
      icon={<WalletCards className='h-4 w-4' />}
      iconTone='success'
      disableHoverEffect
      action={
        props.onOpenBilling ? (
          <Button
            variant='outline'
            size='sm'
            onClick={props.onOpenBilling}
            className='w-full gap-2 sm:w-auto'
          >
            <Receipt className='h-4 w-4' />
            {t('Order History')}
          </Button>
        ) : null
      }
      contentClassName='space-y-4 sm:space-y-6'
    >
      {hasMethods || props.methodsLoading ? (
        <div className='space-y-4 sm:space-y-6'>
          {presets.length > 0 && (
            <div className='space-y-2.5 sm:space-y-3'>
              <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
                {t('Amount')}
              </Label>
              <div className='grid grid-cols-2 gap-1.5 sm:gap-3 md:grid-cols-4'>
                {presets.map((preset) => {
                  const discount = getAmountDiscount(info, preset * 100)
                  const selected = amount === preset * 100
                  return (
                    <Button
                      key={preset}
                      type='button'
                      variant='outline'
                      aria-pressed={selected}
                      className={cn(
                        'flex h-auto min-h-14 min-w-0 flex-col items-start justify-center gap-0.5 rounded-lg px-3 py-2 text-left whitespace-normal',
                        selected
                          ? 'border-foreground bg-foreground/5 dark:border-foreground dark:bg-foreground/10'
                          : 'border-muted'
                      )}
                      onClick={() => setAmountInput(String(preset))}
                    >
                      <span className='max-w-full truncate text-base font-semibold sm:text-lg'>
                        {formatMinorAmount(preset * 100, currency, locale)}
                      </span>
                      {discount < 1 && (
                        <span className='text-success text-xs font-medium'>
                          {getDiscountLabel(discount, t)}
                        </span>
                      )}
                    </Button>
                  )
                })}
              </div>
            </div>
          )}

          <div className='space-y-2.5 sm:space-y-3'>
            <Label
              htmlFor='topup-amount'
              className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
            >
              {t('Custom Amount ({{currency}})', { currency })}
            </Label>
            <Input
              id='topup-amount'
              inputMode='decimal'
              value={amountText}
              onChange={(e) => setAmountInput(e.target.value)}
              placeholder={t('Minimum {{amount}}', {
                amount: formatMinorAmount(minimum, currency, locale),
              })}
              aria-invalid={amount === null || belowMinimum}
              className='h-9 text-base sm:h-10 sm:text-lg'
            />
            {amountText !== '' && amount === null && (
              <p className='text-destructive text-xs'>
                {t('Enter an amount with at most two decimal places')}
              </p>
            )}
            {belowMinimum && (
              <p className='text-destructive text-xs'>
                {t('Minimum topup amount: {{amount}}', {
                  amount: formatMinorAmount(minimum, currency, locale),
                })}
              </p>
            )}
          </div>

          <div className='space-y-2.5 sm:space-y-3'>
            <Label className='text-muted-foreground text-xs font-medium tracking-wider uppercase'>
              {t('Payment Method')}
            </Label>
            <PaymentMethodPicker
              methods={props.methods}
              selectedId={selectedMethod?.id}
              onSelect={(method) => setMethodId(method.id)}
              loading={props.methodsLoading}
              disabled={props.purchasing}
              getDisabledReason={(method) =>
                takes(method)
                  ? undefined
                  : t('Takes {{amounts}}', {
                      amounts: describeAmountRanges(
                        method.amount_ranges,
                        currency,
                        locale,
                        t
                      ),
                    })
              }
            />
            {noMethodTakesAmount && (
              <Alert>
                <AlertDescription>
                  {t(
                    'No payment method takes this amount. Choose another amount.'
                  )}
                </AlertDescription>
              </Alert>
            )}
          </div>

          {request && (
            <PaymentQuoteSummary
              quote={quoteSettled ? quote.data : undefined}
              loading={!quoteSettled}
              error={quoteRequest === null ? null : quote.error}
            />
          )}

          <Button
            className='w-full'
            disabled={!request || !quoteSettled || props.purchasing}
            onClick={() => request && void props.onPurchase(request)}
          >
            {props.purchasing && <Loader2 className='h-4 w-4 animate-spin' />}
            {quoteSettled && quote.data
              ? t('Pay {{amount}}', {
                  amount: formatMinorAmount(
                    quote.data.amount,
                    quote.data.currency,
                    locale
                  ),
                })
              : t('Pay')}
          </Button>
        </div>
      ) : (
        <Alert>
          <AlertDescription>
            {t(
              'Online topup is not enabled. Please use redemption code or contact administrator.'
            )}
          </AlertDescription>
        </Alert>
      )}

      {/* Redemption Code Section */}
      <div className='space-y-2.5 border-t pt-4 sm:space-y-3 sm:pt-6'>
        <div className='flex items-center gap-2'>
          <IconBadge tone='warning' size='xs'>
            <Gift />
          </IconBadge>
          <Label
            htmlFor='redemption-code'
            className='text-muted-foreground text-xs font-medium tracking-wider uppercase'
          >
            {t('Have a Code?')}
          </Label>
        </div>
        <div className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'>
          <Input
            id='redemption-code'
            value={props.redemptionCode}
            onChange={(e) => props.onRedemptionCodeChange(e.target.value)}
            placeholder={t('Enter your redemption code')}
            className='h-9 min-w-0'
          />
          <Button
            onClick={props.onRedeem}
            disabled={props.redeeming}
            variant='outline'
            className='h-9 px-4'
          >
            {props.redeeming && (
              <Loader2 className='mr-2 h-4 w-4 animate-spin' />
            )}
            {t('Redeem')}
          </Button>
        </div>
        {props.topupLink && (
          <p className='text-muted-foreground text-xs'>
            {t('Need a redemption code?')}{' '}
            <a
              href={props.topupLink}
              target='_blank'
              rel='noopener noreferrer'
              className='inline-flex items-center gap-1 underline-offset-4 hover:underline'
            >
              {t('Get one here')}
              <ExternalLink className='h-3 w-3' />
            </a>
          </p>
        )}
      </div>
    </TitledCard>
  )
}
