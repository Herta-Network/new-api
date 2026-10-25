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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { StatusBadge } from '@/components/status-badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampToDate } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { listPaymentOrderInvoices } from '../api'
import {
  PAYMENT_INVOICE_CLOSE_REASONS,
  PAYMENT_INVOICE_STATUSES,
} from '../constants'
import { formatOrderAmount, paymentPlatformLabel } from '../lib/format'
import type { PaymentOrder } from '../types'

interface OrderInvoicesDialogProps {
  /** The order whose invoices are shown; null closes the dialog. */
  order: PaymentOrder | null
  onClose: () => void
}

/**
 * The invoices of an order: each asked one payment method to collect it,
 * and the first one paid completed it.
 */
export function OrderInvoicesDialog(props: OrderInvoicesDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const orderNo = props.order?.order_no ?? ''
  const invoices = useQuery({
    queryKey: ['payment-orders', 'invoices', orderNo],
    queryFn: async () =>
      requireServerSuccess(await listPaymentOrderInvoices(orderNo)).data,
    enabled: orderNo !== '',
    meta: { errorToast: false },
  })

  return (
    <Dialog
      open={props.order !== null}
      onOpenChange={(open) => !open && props.onClose()}
      title={t('Invoices of order {{orderNo}}', { orderNo })}
      description={t(
        'Each invoice asks one payment method to collect the order; the first one paid completes it.'
      )}
      contentClassName='sm:max-w-4xl'
      contentHeight='auto'
    >
      {invoices.isLoading && <LoadingState />}
      {invoices.isError && (
        <ErrorState onRetry={() => void invoices.refetch()} />
      )}
      {invoices.data && invoices.data.length === 0 && (
        <EmptyState title={t('This order has no invoices.')} />
      )}
      {invoices.data && invoices.data.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Invoice number')}</TableHead>
              <TableHead>{t('Platform')}</TableHead>
              <TableHead>{t('Charged')}</TableHead>
              <TableHead>{t('Trade number')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Created')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {invoices.data.map((invoice) => {
              const status = PAYMENT_INVOICE_STATUSES[invoice.status]
              const closeReason =
                PAYMENT_INVOICE_CLOSE_REASONS[invoice.close_reason]
              return (
                <TableRow key={invoice.id}>
                  <TableCell className='font-mono text-xs'>
                    {invoice.invoice_no}
                  </TableCell>
                  <TableCell>
                    {paymentPlatformLabel(invoice.platform, t)}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {invoice.payment_currency
                      ? formatOrderAmount(
                          invoice.payment_amount,
                          invoice.payment_currency,
                          locale
                        )
                      : '-'}
                  </TableCell>
                  <TableCell className='max-w-48 truncate font-mono text-xs'>
                    {invoice.trade_no || '-'}
                  </TableCell>
                  <TableCell>
                    <span className='flex flex-col gap-0.5'>
                      <StatusBadge
                        label={
                          status ? t(status.labelKey) : String(invoice.status)
                        }
                        variant={status?.variant ?? 'neutral'}
                        copyable={false}
                        className='-ml-1.5'
                      />
                      {closeReason && (
                        <span className='text-muted-foreground text-xs'>
                          {t(closeReason)}
                        </span>
                      )}
                    </span>
                  </TableCell>
                  <TableCell className='font-mono text-xs whitespace-nowrap'>
                    {formatTimestampToDate(invoice.created_at)}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </Dialog>
  )
}
