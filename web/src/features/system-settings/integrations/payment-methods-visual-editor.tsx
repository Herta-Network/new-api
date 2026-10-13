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
import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { ReactIconByName } from '@/components/react-icon-by-name'
import { Button } from '@/components/ui/button'

import {
  deletePaymentMethod,
  getPaymentMethods,
  savePaymentMethod,
  type PaymentMethodData,
} from './payment-method-api'

const PaymentMethodDialog = lazy(() =>
  import('./payment-method-dialog').then((module) => ({
    default: module.PaymentMethodDialog,
  }))
)

export function PaymentMethodsVisualEditor() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [editing, setEditing] = useState<PaymentMethodData | null | undefined>()
  const [deleting, setDeleting] = useState<PaymentMethodData | null>(null)
  const methods = useQuery({
    queryKey: ['payment-methods'],
    queryFn: getPaymentMethods,
  })
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['payment-methods'] })
  }
  const save = useMutation({
    mutationFn: savePaymentMethod,
    onSuccess: () => {
      setEditing(undefined)
      refresh()
      toast.success(t('Saved successfully'))
    },
  })
  const remove = useMutation({
    mutationFn: deletePaymentMethod,
    onSuccess: () => {
      setDeleting(null)
      refresh()
      toast.success(t('Deleted successfully'))
    },
  })

  return (
    <div className='space-y-4'>
      <div className='flex items-center justify-between gap-3'>
        <h3 className='font-medium'>{t('Payment methods')}</h3>
        <Button
          type='button'
          variant='outline'
          onClick={() => setEditing(null)}
        >
          {t('Add payment method')}
        </Button>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Disabling or deleting a payment method stops new payments. Existing orders can still complete.'
        )}
      </p>
      {methods.isPending && <LoadingState />}
      {methods.isError && <ErrorState onRetry={() => void methods.refetch()} />}
      {methods.isSuccess && (
        <StaticDataTable
          data={methods.data}
          getRowKey={(method) => method.id}
          emptyContent={t('No payment methods configured')}
          columns={[
            {
              id: 'name',
              header: t('Name'),
              cell: (method) => (
                <div className='flex items-center gap-2'>
                  <ReactIconByName
                    name={method.icon}
                    className='size-4 shrink-0'
                  />
                  <span>{method.name}</span>
                </div>
              ),
            },
            {
              id: 'merchant',
              header: t('Epay merchant ID'),
              cell: (method) => method.config.merchant_id,
            },
            {
              id: 'type',
              header: t('Payment type key'),
              cell: (method) => method.config.payment_type,
            },
            {
              id: 'enabled',
              header: t('Status'),
              cell: (method) => (method.enabled ? t('Enabled') : t('Disabled')),
            },
            {
              id: 'actions',
              header: t('Actions'),
              cell: (method) => (
                <StaticRowActions
                  editLabel={t('Edit payment method')}
                  deleteLabel={t('Delete')}
                  menuLabel={t('Actions')}
                  onEdit={() => setEditing(method)}
                  onDelete={() => setDeleting(method)}
                />
              ),
            },
          ]}
        />
      )}
      {editing !== undefined && (
        <Suspense fallback={<LoadingState />}>
          <PaymentMethodDialog
            editData={editing}
            saving={save.isPending}
            onOpenChange={(open) => {
              if (!open) setEditing(undefined)
            }}
            onSave={(method) => save.mutate(method)}
          />
        </Suspense>
      )}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open && !remove.isPending) setDeleting(null)
        }}
        title={t('Delete payment method')}
        desc={t(
          'Disabling or deleting a payment method stops new payments. Existing orders can still complete.'
        )}
        destructive
        isLoading={remove.isPending}
        handleConfirm={() => {
          if (deleting) remove.mutate(deleting.id)
        }}
      />
    </div>
  )
}
