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
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { formatMinorAmount } from '@/lib/currency'
import { formatQuota } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import type { PaymentQuote } from '../types'

interface PaymentQuoteSummaryProps {
  quote: PaymentQuote | undefined
  loading: boolean
  error: unknown
  /** Whether the quote is paid from the balance (shows the quota deducted) */
  balance?: boolean
}

/**
 * The server's price of a purchase: base price, the group or discount
 * adjustment, the method's fee and the total, plus what the gateway collects
 * when it charges another currency.
 */
export function PaymentQuoteSummary(props: PaymentQuoteSummaryProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  if (props.error) {
    return (
      <Alert variant='destructive'>
        <AlertDescription>
          {getServerErrorMessage(props.error, t('Failed to calculate price'))}
        </AlertDescription>
      </Alert>
    )
  }
  if (props.loading || !props.quote) {
    return <Skeleton className='h-24 w-full rounded-lg' />
  }

  const quote = props.quote
  const money = (amount: number) =>
    formatMinorAmount(amount, quote.currency, locale)
  const chargedElsewhere =
    !!quote.payment_currency && quote.payment_currency !== quote.currency

  return (
    <dl
      aria-label={t('Price details')}
      className='bg-muted/30 space-y-2 rounded-lg border p-3 text-sm'
    >
      <div className='flex items-center justify-between gap-2'>
        <dt className='text-muted-foreground'>{t('Price')}</dt>
        <dd>{money(quote.price)}</dd>
      </div>
      {quote.adjustment !== 0 && (
        <div className='flex items-center justify-between gap-2'>
          <dt className='text-muted-foreground'>
            {quote.adjustment < 0 ? t('Discount') : t('Price adjustment')}
          </dt>
          <dd className={quote.adjustment < 0 ? 'text-success' : undefined}>
            {quote.adjustment > 0 && '+'}
            {money(quote.adjustment)}
          </dd>
        </div>
      )}
      {quote.fee_amount > 0 && (
        <div className='flex items-center justify-between gap-2'>
          <dt className='text-muted-foreground'>{t('Handling fee')}</dt>
          <dd>+{money(quote.fee_amount)}</dd>
        </div>
      )}
      <Separator />
      <div className='flex items-center justify-between gap-2'>
        <dt className='font-medium'>{t('Total')}</dt>
        <dd className='text-base font-semibold'>{money(quote.amount)}</dd>
      </div>
      {chargedElsewhere && (
        <div className='flex items-center justify-between gap-2 text-xs'>
          <dt className='text-muted-foreground'>{t('Charged as')}</dt>
          <dd>
            {formatMinorAmount(
              quote.payment_amount,
              quote.payment_currency,
              locale
            )}
          </dd>
        </div>
      )}
      {quote.purpose === 'topup' && (
        <div className='flex items-center justify-between gap-2 text-xs'>
          <dt className='text-muted-foreground'>{t('You receive')}</dt>
          <dd>{formatQuota(quote.quota_amount)}</dd>
        </div>
      )}
      {props.balance && (
        <div className='flex items-center justify-between gap-2 text-xs'>
          <dt className='text-muted-foreground'>{t('Balance deducted')}</dt>
          <dd>{formatQuota(quote.quota_amount)}</dd>
        </div>
      )}
    </dl>
  )
}
