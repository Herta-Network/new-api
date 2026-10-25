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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  DataTablePage,
  useDataTable,
  useDebouncedColumnFilter,
} from '@/components/data-table'
import { Input } from '@/components/ui/input'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { requireServerSuccess } from '@/lib/server-error-message'

import { runPaymentOrderAction, type PaymentOrderAction } from '../api'
import {
  PAYMENT_ORDER_STATUS,
  PAYMENT_ORDER_STATUSES,
  PAYMENT_PLATFORM_IDS,
  PAYMENT_PURPOSES,
} from '../constants'
import { paymentPlatformLabel } from '../lib/format'
import {
  PAYMENT_ORDERS_TABLE_URL_CONFIG,
  paymentOrdersListQueryOptions,
  paymentOrdersQueryKey,
} from '../lib/payment-orders-query'
import type { PaymentOrder } from '../types'
import { OrderInvoicesDialog } from './order-invoices-dialog'
import { usePaymentOrdersColumns } from './orders-columns'
import { PaymentNoteDialog } from './payment-note-dialog'

const route = getRouteApi('/_authenticated/admin/payment-orders/$section')

type ActionRequest = {
  order: PaymentOrder
  action: PaymentOrderAction
  note?: string
}

/** What an action did, judged by the order the server returns. */
function actionResultMessage(action: PaymentOrderAction, order: PaymentOrder) {
  switch (action) {
    case 'reconcile':
      if (order.status === PAYMENT_ORDER_STATUS.FINISHED) {
        return 'The gateway confirmed the payment and the order is completed'
      }
      if (order.status === PAYMENT_ORDER_STATUS.PAID) {
        return 'The gateway confirmed the payment, but the order could not be fulfilled'
      }
      return 'The gateway reports no payment for this order yet'
    case 'close':
      return order.status === PAYMENT_ORDER_STATUS.CLOSED
        ? 'Order closed'
        : 'The gateway reported a payment, so the order was settled instead'
    case 'fulfil':
      return 'Order fulfilled'
    case 'complete':
      return 'Order marked as paid'
  }
}

export function PaymentOrdersTable() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<ActionRequest | null>(null)
  const [invoicesOf, setInvoicesOf] = useState<PaymentOrder | null>(null)

  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    ...PAYMENT_ORDERS_TABLE_URL_CONFIG,
  })
  const userFilter = useDebouncedColumnFilter({
    columnFilters,
    columnId: 'user_id',
    onColumnFiltersChange,
  })

  const { data, isLoading, isFetching } = useQuery({
    ...paymentOrdersListQueryOptions({
      globalFilter,
      columnFilters,
      pagination,
    }),
    placeholderData: (previous) => previous,
  })

  const runAction = useMutation({
    mutationFn: async (request: ActionRequest) =>
      requireServerSuccess(
        await runPaymentOrderAction(
          request.order.order_no,
          request.action,
          request.note
        )
      ).data,
    onSuccess: (order, request) => {
      toast.success(t(actionResultMessage(request.action, order)))
      setTarget(null)
      void queryClient.invalidateQueries({ queryKey: paymentOrdersQueryKey })
    },
  })

  const columns = usePaymentOrdersColumns((order, action) => {
    if (action === 'reconcile') {
      runAction.mutate({ order, action })
      return
    }
    setTarget({ order, action })
  }, setInvoicesOf)
  const { table } = useDataTable({
    data: data?.items ?? [],
    columns,
    getRowId: (row) => row.order_no,
    columnFilters,
    globalFilter,
    pagination,
    onPaginationChange,
    onGlobalFilterChange,
    onColumnFiltersChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: data?.total ?? 0,
    ensurePageInRange,
  })

  const closeDialog = (open: boolean) => {
    if (!open) setTarget(null)
  }
  const orderNo = target?.order.order_no ?? ''

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No orders found')}
        emptyDescription={t(
          'Orders appear here when users top up or buy a plan.'
        )}
        skeletonKeyPrefix='payment-orders-skeleton'
        applyHeaderSize
        toolbarProps={{
          searchPlaceholder: t('Filter by order number...'),
          searchDebounceMs: 500,
          additionalSearch: (
            <Input
              inputMode='numeric'
              placeholder={t('Filter by user ID...')}
              aria-label={t('Filter by user ID...')}
              value={userFilter.inputValue}
              onChange={userFilter.onChange}
              className='w-full sm:w-40'
            />
          ),
          hasAdditionalFilters: userFilter.value !== '',
          onReset: userFilter.resetInput,
          filters: [
            {
              columnId: 'status',
              title: t('Status'),
              singleSelect: true,
              options: Object.entries(PAYMENT_ORDER_STATUSES).map(
                ([value, status]) => ({ value, label: t(status.labelKey) })
              ),
            },
            {
              columnId: 'purpose',
              title: t('Purpose'),
              singleSelect: true,
              options: Object.entries(PAYMENT_PURPOSES).map(
                ([value, labelKey]) => ({ value, label: t(labelKey) })
              ),
            },
            {
              columnId: 'platform',
              title: t('Platform'),
              singleSelect: true,
              options: PAYMENT_PLATFORM_IDS.map((value) => ({
                value,
                label: paymentPlatformLabel(value, t),
              })),
            },
          ],
        }}
      />

      <OrderInvoicesDialog
        order={invoicesOf}
        onClose={() => setInvoicesOf(null)}
      />

      <ConfirmDialog
        open={target?.action === 'close'}
        onOpenChange={closeDialog}
        title={t('Close order')}
        desc={t(
          'Close order {{orderNo}}? The gateway is asked first: if it reports a payment, the order is settled instead.',
          { orderNo }
        )}
        confirmText={t('Close order')}
        destructive
        isLoading={runAction.isPending}
        handleConfirm={() => target && runAction.mutate(target)}
      />

      <ConfirmDialog
        open={target?.action === 'fulfil'}
        onOpenChange={closeDialog}
        title={t('Fulfil again')}
        desc={t(
          'Credit the purchase of order {{orderNo}} again? Fix the cause of the failed fulfilment first.',
          { orderNo }
        )}
        confirmText={t('Fulfil again')}
        isLoading={runAction.isPending}
        handleConfirm={() => target && runAction.mutate(target)}
      />

      {target?.action === 'complete' && (
        <PaymentNoteDialog
          open
          onOpenChange={closeDialog}
          title={t('Mark as paid')}
          desc={t(
            'Mark order {{orderNo}} as paid without gateway evidence and fulfil it, for example after a bank transfer. Your note is kept in the audit log.',
            { orderNo }
          )}
          confirmText={t('Mark as paid')}
          destructive
          isLoading={runAction.isPending}
          onConfirm={(note) => runAction.mutate({ ...target, note })}
        />
      )}
    </>
  )
}
