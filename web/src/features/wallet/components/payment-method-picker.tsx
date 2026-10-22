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

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

import { STRIPE_PAYMENT_METHOD_TYPE_LABELS } from '../constants'
import { getPaymentIcon } from '../lib/ui'
import type { PaymentMethodOption } from '../types'

interface PaymentMethodPickerProps {
  methods: PaymentMethodOption[]
  selectedId: number | undefined
  onSelect: (method: PaymentMethodOption) => void
  loading?: boolean
  disabled?: boolean
  /** An extra reason (i18n text) a method cannot be chosen here */
  getDisabledReason?: (method: PaymentMethodOption) => string | undefined
}

/**
 * The payment methods of a purchase as a group of toggle buttons. An
 * unavailable method stays listed, disabled, with the server's reason; a
 * Stripe method names the payment method type it collects.
 */
export function PaymentMethodPicker(props: PaymentMethodPickerProps) {
  const { t } = useTranslation()

  if (props.loading) {
    return (
      <div className='grid grid-cols-2 gap-1.5 sm:gap-3 lg:grid-cols-3'>
        {['first', 'second', 'third'].map((key) => (
          <Skeleton key={key} className='h-14 rounded-lg' />
        ))}
      </div>
    )
  }

  return (
    <div
      role='group'
      aria-label={t('Payment Method')}
      className='grid grid-cols-2 gap-1.5 sm:gap-3 lg:grid-cols-3'
    >
      {props.methods.map((method) => {
        const reason = method.available
          ? props.getDisabledReason?.(method)
          : t(
              method.unavailable?.key ?? 'This payment method is unavailable',
              method.unavailable?.params
            )
        const selected = props.selectedId === method.id
        const paymentType = method.payment_method_type
          ? t(STRIPE_PAYMENT_METHOD_TYPE_LABELS[method.payment_method_type])
          : undefined
        const detail =
          reason ||
          [paymentType, method.description].filter(Boolean).join(' · ')
        return (
          <Button
            key={method.id}
            type='button'
            variant='outline'
            aria-pressed={selected}
            disabled={props.disabled || !!reason}
            title={reason}
            onClick={() => props.onSelect(method)}
            className={cn(
              'h-auto min-h-14 min-w-0 justify-start gap-2 rounded-lg px-3 py-2 text-left whitespace-normal',
              selected &&
                'border-foreground bg-foreground/5 dark:border-foreground dark:bg-foreground/10'
            )}
          >
            <span className='shrink-0'>
              {getPaymentIcon(
                method.platform,
                'h-4 w-4',
                method.icon,
                method.name,
                method.payment_method_type
              )}
            </span>
            <span className='flex min-w-0 flex-col items-start gap-0.5'>
              <span className='max-w-full truncate'>{method.name}</span>
              {detail && (
                <span className='text-muted-foreground line-clamp-2 max-w-full text-[11px] leading-4 font-normal'>
                  {detail}
                </span>
              )}
            </span>
          </Button>
        )
      })}
    </div>
  )
}
