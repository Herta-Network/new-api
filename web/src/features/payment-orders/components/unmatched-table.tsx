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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Button } from '@/components/ui/button'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampToDate } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'

import { resolvePaymentUnmatched } from '../api'
import { PAYMENT_UNMATCHED_STATES } from '../constants'
import { formatOrderAmount, paymentPlatformLabel } from '../lib/format'
import {
  PAYMENT_UNMATCHED_TABLE_URL_CONFIG,
  paymentUnmatchedListQueryOptions,
  paymentUnmatchedQueryKey,
} from '../lib/payment-orders-query'
import type { PaymentUnmatched } from '../types'
import { PaymentNoteDialog } from './payment-note-dialog'

const route = getRouteApi('/_authenticated/admin/payment-orders/$section')

export function PaymentUnmatchedTable() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const [target, setTarget] = useState<PaymentUnmatched | null>(null)

  const {
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    ...PAYMENT_UNMATCHED_TABLE_URL_CONFIG,
  })

  const { data, isLoading, isFetching } = useQuery({
    ...paymentUnmatchedListQueryOptions({ columnFilters, pagination }),
    placeholderData: (previous) => previous,
  })

  const resolve = useMutation({
    mutationFn: async (request: { id: number; note: string }) =>
      requireServerSuccess(
        await resolvePaymentUnmatched(request.id, request.note)
      ),
    onSuccess: () => {
      toast.success(t('Marked as resolved'))
      setTarget(null)
      void queryClient.invalidateQueries({
        queryKey: paymentUnmatchedQueryKey,
      })
    },
  })

  const columns: ColumnDef<PaymentUnmatched>[] = [
    {
      accessorKey: 'id',
      header: t('ID'),
      meta: { mobileHidden: true },
      cell: ({ row }) => <TableId value={row.original.id} />,
      size: 70,
    },
    {
      accessorKey: 'order_no',
      header: t('Order number'),
      meta: { mobileTitle: true },
      cell: ({ row }) => (
        <span className='flex min-w-0 flex-col gap-0.5'>
          <span className='font-mono text-xs'>{row.original.order_no}</span>
          {row.original.invoice_no &&
            row.original.invoice_no !== row.original.order_no && (
              <span className='text-muted-foreground font-mono text-xs'>
                {t('Invoice {{invoiceNo}}', {
                  invoiceNo: row.original.invoice_no,
                })}
              </span>
            )}
        </span>
      ),
      size: 220,
    },
    {
      accessorKey: 'platform',
      header: t('Platform'),
      cell: ({ row }) => paymentPlatformLabel(row.original.platform, t),
      size: 110,
    },
    {
      accessorKey: 'trade_no',
      header: t('Trade number'),
      cell: ({ row }) => (
        <span
          className='block truncate font-mono text-xs'
          title={row.original.trade_no}
        >
          {row.original.trade_no}
        </span>
      ),
      size: 180,
    },
    {
      accessorKey: 'user_id',
      header: t('User ID'),
      cell: ({ row }) => <TableId value={row.original.user_id} />,
      size: 90,
    },
    {
      accessorKey: 'amount',
      header: t('Amount'),
      cell: ({ row }) => (
        <span className='whitespace-nowrap tabular-nums'>
          {formatOrderAmount(
            row.original.amount,
            row.original.currency,
            locale
          )}
        </span>
      ),
      size: 130,
    },
    {
      accessorKey: 'reason',
      header: t('Reason'),
      cell: ({ row }) => (
        <span className='line-clamp-2 text-sm' title={row.original.reason}>
          {row.original.reason}
        </span>
      ),
      size: 240,
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
      accessorKey: 'resolved_at',
      header: t('Status'),
      meta: { mobileBadge: true },
      cell: ({ row }) => {
        const record = row.original
        if (record.resolved_at === 0) {
          return (
            <StatusBadge
              label={t('Unresolved')}
              variant='warning'
              copyable={false}
              className='-ml-1.5'
            />
          )
        }
        return (
          <span className='flex min-w-0 flex-col gap-0.5'>
            <StatusBadge
              label={t('Resolved')}
              variant='success'
              copyable={false}
              className='-ml-1.5'
            />
            <span
              className='text-muted-foreground truncate text-xs'
              title={record.resolve_note}
            >
              {formatTimestampToDate(record.resolved_at)}
              {record.resolve_note ? ` · ${record.resolve_note}` : ''}
            </span>
          </span>
        )
      },
      size: 200,
    },
    {
      id: 'actions',
      header: () => t('Actions'),
      meta: { pinned: 'right' as const },
      cell: ({ row }) =>
        row.original.resolved_at === 0 ? (
          <Button
            variant='outline'
            size='sm'
            onClick={() => setTarget(row.original)}
          >
            {t('Resolve')}
          </Button>
        ) : null,
      size: 100,
    },
  ]

  const { table } = useDataTable({
    data: data?.items ?? [],
    columns,
    getRowId: (row) => String(row.id),
    columnFilters,
    pagination,
    onPaginationChange,
    onColumnFiltersChange,
    manualPagination: true,
    manualFiltering: true,
    totalCount: data?.total ?? 0,
    ensurePageInRange,
  })

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No unmatched payments')}
        emptyDescription={t(
          'Payments a gateway confirmed but that could not settle their order appear here.'
        )}
        skeletonKeyPrefix='payment-unmatched-skeleton'
        applyHeaderSize
        toolbarProps={{
          customSearch: (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Refund or fulfil these payments by hand, then mark them resolved.'
              )}
            </p>
          ),
          filters: [
            {
              columnId: 'resolved_at',
              title: t('Status'),
              singleSelect: true,
              options: PAYMENT_UNMATCHED_STATES.map((state) => ({
                value: state.value,
                label: t(state.labelKey),
              })),
            },
          ],
        }}
      />

      {target && (
        <PaymentNoteDialog
          open
          onOpenChange={(open) => !open && setTarget(null)}
          title={t('Resolve unmatched payment')}
          desc={t(
            'Mark the payment {{tradeNo}} of order {{orderNo}} as handled. Describe what you did, such as a refund.',
            { tradeNo: target.trade_no, orderNo: target.order_no }
          )}
          confirmText={t('Resolve')}
          isLoading={resolve.isPending}
          onConfirm={(note) => resolve.mutate({ id: target.id, note })}
        />
      )}
    </>
  )
}
