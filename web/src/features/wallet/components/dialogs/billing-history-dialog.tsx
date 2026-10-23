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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Search, ChevronLeft, ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { toIntlLocale } from '@/i18n/languages'
import { formatMinorAmount } from '@/lib/currency'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { closePaymentOrder } from '../../api'
import { useBillingHistory } from '../../hooks/use-billing-history'
import {
  getOrderStatusConfig,
  getPlatformLabel,
  isOrderPayable,
} from '../../lib/billing'
import { paymentQueryKeys } from '../../lib/wallet-queries'

interface BillingHistoryDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pay a pending order again */
  onResume?: (orderNo: string) => void
  resuming?: boolean
}

/** Page sizes; labels are i18n keys */
const PAGE_SIZE_OPTIONS = [
  { value: '10', label: '10 / page' },
  { value: '20', label: '20 / page' },
  { value: '50', label: '50 / page' },
  { value: '100', label: '100 / page' },
]

export function BillingHistoryDialog(props: BillingHistoryDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const history = useBillingHistory(props.open)
  const [closingOrderNo, setClosingOrderNo] = useState<string | null>(null)
  const nowSeconds = Date.now() / 1000

  const closeOrder = useMutation({
    mutationFn: closePaymentOrder,
    onSuccess: (order) => {
      toast.success(t('Order cancelled'))
      setClosingOrderNo(null)
      queryClient.setQueryData(paymentQueryKeys.order(order.order_no), order)
      void queryClient.invalidateQueries({ queryKey: paymentQueryKeys.orders })
    },
    onError: (error) => handleServerError(error, t('Failed to cancel order')),
  })

  const totalPages = Math.max(1, Math.ceil(history.total / history.pageSize))

  return (
    <>
      <Dialog
        open={props.open}
        onOpenChange={props.onOpenChange}
        title={t('Billing History')}
        description={t(
          'View your topup transaction records and payment history'
        )}
        contentClassName='flex max-h-(--dialog-available-height) flex-col max-sm:w-screen max-sm:max-w-none max-sm:rounded-none max-sm:p-4 sm:max-w-4xl'
        contentHeight='auto'
        bodyClassName='space-y-3'
      >
        <div className='min-h-0 space-y-3'>
          <div className='flex items-center gap-2'>
            <div className='relative flex-1'>
              <Search className='text-muted-foreground absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2' />
              <Input
                placeholder={t('Search by order number...')}
                aria-label={t('Search by order number...')}
                value={history.keyword}
                onChange={(e) => history.handleSearch(e.target.value)}
                className='h-9 pl-10'
              />
            </div>
            <Select
              items={PAGE_SIZE_OPTIONS.map((option) => ({
                value: option.value,
                label: t(option.label),
              }))}
              value={String(history.pageSize)}
              onValueChange={(value) =>
                value !== null &&
                history.handlePageSizeChange(Number.parseInt(value))
              }
            >
              <SelectTrigger className='h-9 w-[92px] sm:w-32'>
                <SelectValue />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {PAGE_SIZE_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>

          <div className='max-h-[min(54vh,520px)] overflow-y-auto pr-1'>
            {history.loading && (
              <div className='space-y-3'>
                {['first', 'second', 'third'].map((placeholder) => (
                  <div
                    key={placeholder}
                    className='rounded-lg border p-3 sm:p-4'
                  >
                    <Skeleton className='h-4 w-48' />
                    <Skeleton className='mt-2 h-3 w-32' />
                    <div className='mt-3 grid grid-cols-2 gap-3 sm:grid-cols-4'>
                      <Skeleton className='h-3 w-full' />
                      <Skeleton className='h-3 w-full' />
                    </div>
                  </div>
                ))}
              </div>
            )}
            {!history.loading && history.records.length === 0 && (
              <div className='text-muted-foreground flex min-h-40 flex-col items-center justify-center py-10 text-center'>
                <p className='text-sm font-medium'>
                  {t('No billing records found')}
                </p>
                <p className='mt-1 text-xs'>
                  {history.searching
                    ? t('Try adjusting your search')
                    : t('Your transaction history will appear here')}
                </p>
              </div>
            )}
            {!history.loading && history.records.length > 0 && (
              <ul className='space-y-3'>
                {history.records.map((order) => {
                  const status = getOrderStatusConfig(order.status)
                  const payable = isOrderPayable(order, nowSeconds)
                  return (
                    <li
                      key={order.order_no}
                      className='rounded-lg border p-3 sm:p-4'
                    >
                      <div className='flex items-start justify-between gap-2'>
                        <div className='min-w-0 flex-1 space-y-1'>
                          <div className='truncate text-sm font-medium'>
                            {order.subject}
                          </div>
                          <div className='flex min-w-0 items-center gap-1'>
                            <code className='text-muted-foreground truncate font-mono text-xs'>
                              {order.order_no}
                            </code>
                            <CopyButton
                              value={order.order_no}
                              size='sm'
                              className='h-5 w-5 p-0'
                              iconClassName='h-3 w-3'
                            />
                          </div>
                          <div className='text-muted-foreground text-xs'>
                            {formatTimestampToDate(order.created_at)}
                          </div>
                        </div>
                        <StatusBadge
                          label={t(status.label)}
                          variant={status.variant}
                          copyable={false}
                        />
                      </div>

                      <dl className='mt-3 grid grid-cols-2 gap-3 sm:mt-4 sm:grid-cols-4 sm:gap-4'>
                        <div className='space-y-1'>
                          <dt className='text-muted-foreground text-xs'>
                            {t('Type')}
                          </dt>
                          <dd className='text-sm font-medium'>
                            {order.purpose === 'topup'
                              ? t('Top-up')
                              : t('Subscription')}
                          </dd>
                        </div>
                        <div className='space-y-1'>
                          <dt className='text-muted-foreground text-xs'>
                            {t('Payment Method')}
                          </dt>
                          <dd className='text-sm font-medium'>
                            {t(getPlatformLabel(order.platform))}
                          </dd>
                        </div>
                        <div className='space-y-1'>
                          <dt className='text-muted-foreground text-xs'>
                            {t('Total')}
                          </dt>
                          <dd className='text-sm font-semibold'>
                            {formatMinorAmount(
                              order.amount,
                              order.currency,
                              locale
                            )}
                          </dd>
                        </div>
                        {order.purpose === 'topup' && (
                          <div className='space-y-1'>
                            <dt className='text-muted-foreground text-xs'>
                              {t('Quota')}
                            </dt>
                            <dd className='text-sm font-medium'>
                              {formatQuota(order.quota_amount)}
                            </dd>
                          </div>
                        )}
                      </dl>

                      {payable && (
                        <div className='mt-4 flex justify-end gap-2'>
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => setClosingOrderNo(order.order_no)}
                          >
                            {t('Cancel order')}
                          </Button>
                          {props.onResume && (
                            <Button
                              size='sm'
                              onClick={() => props.onResume?.(order.order_no)}
                              disabled={props.resuming}
                            >
                              {t('Continue payment')}
                            </Button>
                          )}
                        </div>
                      )}
                    </li>
                  )
                })}
              </ul>
            )}
          </div>

          {!history.loading &&
            !history.searching &&
            history.records.length > 0 && (
              <div className='flex flex-col items-center gap-3 border-t pt-4 sm:flex-row sm:items-center sm:justify-between'>
                <div className='text-muted-foreground text-xs sm:text-sm'>
                  {t('Showing')} {(history.page - 1) * history.pageSize + 1}-
                  {Math.min(history.page * history.pageSize, history.total)}{' '}
                  {t('of')} {history.total}
                </div>
                <div className='flex items-center gap-2'>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => history.handlePageChange(history.page - 1)}
                    disabled={history.page <= 1}
                    className='h-8 w-8 p-0'
                    aria-label={t('Previous page')}
                  >
                    <ChevronLeft className='h-4 w-4' />
                  </Button>
                  <div className='text-muted-foreground flex items-center gap-1 text-sm'>
                    <span className='font-medium'>{history.page}</span>
                    <span>/</span>
                    <span>{totalPages}</span>
                  </div>
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => history.handlePageChange(history.page + 1)}
                    disabled={history.page >= totalPages}
                    className='h-8 w-8 p-0'
                    aria-label={t('Next page')}
                  >
                    <ChevronRight className='h-4 w-4' />
                  </Button>
                </div>
              </div>
            )}
        </div>
      </Dialog>

      <ConfirmDialog
        open={!!closingOrderNo}
        onOpenChange={(open) => !open && setClosingOrderNo(null)}
        title={t('Cancel order')}
        desc={t(
          'Cancel this unpaid order? If you already paid, the payment is still checked before the order closes.'
        )}
        confirmText={t('Cancel order')}
        destructive
        isLoading={closeOrder.isPending}
        handleConfirm={() =>
          closingOrderNo && closeOrder.mutate(closingOrderNo)
        }
      />
    </>
  )
}
