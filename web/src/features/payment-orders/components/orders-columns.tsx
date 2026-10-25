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
import type { ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampToDate } from '@/lib/format'

import type { PaymentOrderAction } from '../api'
import {
  PAYMENT_CLOSE_REASONS,
  PAYMENT_ORDER_STATUS,
  PAYMENT_ORDER_STATUSES,
  PAYMENT_PURPOSES,
} from '../constants'
import { formatOrderAmount, paymentPlatformLabel } from '../lib/format'
import type { PaymentOrder } from '../types'

/** The actions the server accepts for an order in its current state. */
export function availablePaymentOrderActions(
  order: PaymentOrder
): PaymentOrderAction[] {
  const actions: PaymentOrderAction[] = []
  const open =
    order.status === PAYMENT_ORDER_STATUS.PENDING ||
    order.status === PAYMENT_ORDER_STATUS.CLOSED
  if (open && order.platform !== 'balance') actions.push('reconcile')
  if (order.status === PAYMENT_ORDER_STATUS.PENDING) actions.push('close')
  if (order.status === PAYMENT_ORDER_STATUS.PAID) actions.push('fulfil')
  if (open && !order.trade_no) actions.push('complete')
  return actions
}

const ACTION_LABELS: Record<PaymentOrderAction, string> = {
  reconcile: 'Check with gateway',
  close: 'Close order',
  fulfil: 'Fulfil again',
  complete: 'Mark as paid',
}

export function usePaymentOrdersColumns(
  onAction: (order: PaymentOrder, action: PaymentOrderAction) => void,
  /** Shows the invoices of a gateway order. */
  onShowInvoices: (order: PaymentOrder) => void
): ColumnDef<PaymentOrder>[] {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  return [
    {
      accessorKey: 'order_no',
      header: t('Order number'),
      meta: { mobileTitle: true },
      cell: ({ row }) => (
        <span className='flex items-center gap-1'>
          <span className='font-mono text-xs'>{row.original.order_no}</span>
          <CopyButton
            value={row.original.order_no}
            size='icon'
            className='size-6'
            tooltip={t('Copy order number')}
            aria-label={t('Copy order number')}
          />
        </span>
      ),
      size: 240,
    },
    {
      accessorKey: 'user_id',
      header: t('User ID'),
      cell: ({ row }) => <TableId value={row.original.user_id} />,
      size: 90,
    },
    {
      accessorKey: 'purpose',
      header: t('Purpose'),
      cell: ({ row }) => (
        <span className='flex min-w-0 flex-col gap-0.5'>
          <StatusBadge
            label={t(
              PAYMENT_PURPOSES[row.original.purpose] ?? row.original.purpose
            )}
            variant='neutral'
            copyable={false}
            className='-ml-1.5'
          />
          {row.original.subject && (
            <span className='text-muted-foreground truncate text-xs'>
              {row.original.subject}
            </span>
          )}
        </span>
      ),
      size: 160,
    },
    {
      accessorKey: 'amount',
      header: t('Amount'),
      cell: ({ row }) => {
        const order = row.original
        const charged =
          order.payment_currency !== '' &&
          (order.payment_currency !== order.currency ||
            order.payment_amount !== order.amount)
        return (
          <span className='flex flex-col gap-0.5 whitespace-nowrap'>
            <span className='font-medium tabular-nums'>
              {formatOrderAmount(order.amount, order.currency, locale)}
            </span>
            {charged && (
              <span className='text-muted-foreground text-xs tabular-nums'>
                {t('Charged {{amount}}', {
                  amount: formatOrderAmount(
                    order.payment_amount,
                    order.payment_currency,
                    locale
                  ),
                })}
              </span>
            )}
          </span>
        )
      },
      size: 150,
    },
    {
      accessorKey: 'platform',
      header: t('Platform'),
      cell: ({ row }) => paymentPlatformLabel(row.original.platform, t),
      size: 110,
    },
    {
      accessorKey: 'status',
      header: t('Status'),
      meta: { mobileBadge: true },
      cell: ({ row }) => {
        const order = row.original
        const status = PAYMENT_ORDER_STATUSES[order.status]
        const closeReason = PAYMENT_CLOSE_REASONS[order.close_reason]
        return (
          <span className='flex min-w-0 flex-col gap-0.5'>
            <StatusBadge
              label={status ? t(status.labelKey) : String(order.status)}
              variant={status?.variant ?? 'neutral'}
              copyable={false}
              className='-ml-1.5'
            />
            {order.fulfil_error && (
              <span
                className='text-destructive truncate text-xs'
                title={order.fulfil_error}
              >
                {order.fulfil_error}
              </span>
            )}
            {order.status === PAYMENT_ORDER_STATUS.CLOSED && closeReason && (
              <span className='text-muted-foreground text-xs'>
                {t(closeReason)}
              </span>
            )}
          </span>
        )
      },
      size: 170,
    },
    {
      accessorKey: 'trade_no',
      header: t('Trade number'),
      meta: { mobileHidden: true },
      cell: ({ row }) =>
        row.original.trade_no ? (
          <span
            className='block truncate font-mono text-xs'
            title={row.original.trade_no}
          >
            {row.original.trade_no}
          </span>
        ) : (
          <span className='text-muted-foreground'>-</span>
        ),
      size: 180,
    },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      meta: { mobileHidden: true },
      cell: ({ row }) => (
        <span className='font-mono text-xs whitespace-nowrap'>
          {formatTimestampToDate(row.original.created_at)}
        </span>
      ),
      size: 170,
    },
    {
      id: 'actions',
      header: () => t('Actions'),
      meta: { pinned: 'right' as const },
      cell: ({ row }) => {
        const actions = availablePaymentOrderActions(row.original)
        const hasInvoices = row.original.platform !== 'balance'
        if (actions.length === 0 && !hasInvoices) return null
        return (
          <DataTableRowActionMenu ariaLabel={t('Open menu')}>
            {hasInvoices && (
              <DropdownMenuItem onClick={() => onShowInvoices(row.original)}>
                {t('Invoices')}
              </DropdownMenuItem>
            )}
            {actions.map((action) => (
              <DropdownMenuItem
                key={action}
                onClick={() => onAction(row.original, action)}
              >
                {t(ACTION_LABELS[action])}
              </DropdownMenuItem>
            ))}
          </DataTableRowActionMenu>
        )
      },
      size: 60,
    },
  ]
}
