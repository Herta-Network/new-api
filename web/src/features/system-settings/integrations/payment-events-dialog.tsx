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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  StaticDataTable,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { formatDateTimeObject } from '@/lib/time'

import { getPaymentEvents, type PaymentEvent } from './payment-event-api'
import {
  PaymentEventReviewDialog,
  PaymentEventStatusLabel,
} from './payment-event-review-dialog'

const EMPTY_EVENTS: PaymentEvent[] = []

export function PaymentEventsDialog(props: {
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [status, setStatus] = useState('review')
  const [search, setSearch] = useState('')
  const [orderNo, setOrderNo] = useState('')
  const [selected, setSelected] = useState<number | null>(null)
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const query = useQuery({
    queryKey: ['payment-events', status, orderNo, pagination],
    queryFn: ({ signal }) =>
      getPaymentEvents(
        {
          status,
          order_no: orderNo,
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        },
        signal
      ),
    refetchInterval: selected === null ? 10000 : false,
  })
  const { table } = useDataTable({
    data: query.data?.items ?? EMPTY_EVENTS,
    columns: [],
    totalCount: query.data?.total ?? 0,
    manualPagination: true,
    columnFilters: [],
    pagination,
    onPaginationChange: setPagination,
    columnVisibilityStorageKey: false,
    columnSizingStorageKey: false,
  })
  return (
    <Dialog
      open
      onOpenChange={props.onOpenChange}
      title={t('Payment events')}
      contentClassName='sm:max-w-5xl'
    >
      <div className='space-y-4'>
        <div className='flex flex-wrap items-center gap-2'>
          <NativeSelect
            aria-label={t('Status')}
            value={status}
            onChange={(event) => {
              setStatus(event.target.value)
              setPagination((previous) => ({ ...previous, pageIndex: 0 }))
            }}
          >
            <NativeSelectOption value=''>{t('All Status')}</NativeSelectOption>
            <NativeSelectOption value='review'>
              {t('Needs review')}
            </NativeSelectOption>
            <NativeSelectOption value='pending'>
              {t('Pending')}
            </NativeSelectOption>
            <NativeSelectOption value='rejected'>
              {t('Rejected')}
            </NativeSelectOption>
            <NativeSelectOption value='completed'>
              {t('Completed')}
            </NativeSelectOption>
            <NativeSelectOption value='ignored'>
              {t('Ignored')}
            </NativeSelectOption>
            <NativeSelectOption value='resolved'>
              {t('Review closed')}
            </NativeSelectOption>
          </NativeSelect>
          <Input
            className='w-full sm:w-64'
            aria-label={t('Order number')}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <Button
            type='button'
            variant='outline'
            onClick={() => {
              setOrderNo(search.trim())
              setPagination((previous) => ({ ...previous, pageIndex: 0 }))
            }}
          >
            {t('Search')}
          </Button>
          <Button
            type='button'
            variant='outline'
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {t('Refresh')}
          </Button>
        </div>
        {query.isPending && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.isSuccess && (
          <>
            <StaticDataTable
              data={query.data.items}
              getRowKey={(event) => event.id}
              emptyContent={t('No payment events')}
              columns={[
                {
                  id: 'order',
                  header: t('Order number'),
                  cell: (event) => event.order_no,
                },
                {
                  id: 'state',
                  header: t('Status'),
                  cell: (event) => (
                    <PaymentEventStatusLabel status={event.status} />
                  ),
                },
                {
                  id: 'attempts',
                  header: t('Attempts'),
                  cell: (event) => formatNumber(event.attempts, locale),
                },
                {
                  id: 'created',
                  header: t('Created time'),
                  cell: (event) =>
                    formatDateTimeObject(new Date(event.created_at * 1000)),
                },
                {
                  id: 'review',
                  header: t('Actions'),
                  cell: (event) => (
                    <Button
                      type='button'
                      variant='outline'
                      size='sm'
                      onClick={() => setSelected(event.id)}
                    >
                      {t('Review payment')}
                    </Button>
                  ),
                },
              ]}
            />
            <DataTablePagination table={table} compact />
          </>
        )}
      </div>
      {selected !== null && (
        <PaymentEventReviewDialog
          eventId={selected}
          onOpenChange={(open) => {
            if (!open) setSelected(null)
          }}
        />
      )}
    </Dialog>
  )
}
