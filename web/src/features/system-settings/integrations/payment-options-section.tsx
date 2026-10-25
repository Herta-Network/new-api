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
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { BadgeCell } from '@/components/data-table/core/badge-cell'
import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import { getPaymentIcon } from '@/features/wallet/lib/ui'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { SettingsSection } from '../components/settings-section'
import {
  BALANCE_PAYMENT_OPTION_ID,
  PAYMENT_FEE_MODE,
  toPaymentOptionInput,
  useDeletePaymentOption,
  useSavePaymentOption,
  type PaymentOption,
} from './payment-methods-api'
import { PaymentOptionDialog } from './payment-option-dialog'
import { usesFeeRate, usesFixedFee } from './payment-option-form'

type PaymentOptionsSectionProps = {
  options: PaymentOption[]
  loading: boolean
  /** Site currency fixed fees are counted in. */
  currency: string
}

/** The payment methods buyers choose from, with the fee each charges. */
export function PaymentOptionsSection(props: PaymentOptionsSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const saveOption = useSavePaymentOption()
  const deleteOption = useDeletePaymentOption()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<PaymentOption | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<PaymentOption | null>(null)

  const openDialog = (option: PaymentOption | null) => {
    setEditing(option)
    setDialogOpen(true)
  }

  return (
    <SettingsSection title={t('Payment Methods')}>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Buyers choose from the enabled methods. The balance can pay for subscriptions only.'
          )}
        </p>
        <Button size='sm' onClick={() => openDialog(null)}>
          <Plus data-icon='inline-start' />
          {t('Add payment method')}
        </Button>
      </div>

      <StaticDataTable
        data={props.options}
        getRowKey={(option) => option.id}
        tableClassName='min-w-[720px] table-fixed'
        emptyClassName='text-sm'
        emptyContent={
          props.loading
            ? t('Loading...')
            : t('No payment methods configured yet.')
        }
        columns={[
          {
            id: 'name',
            header: t('Name'),
            cell: (option) => (
              <span className='flex items-center gap-2 font-medium'>
                <span
                  className='text-muted-foreground shrink-0'
                  aria-hidden='true'
                >
                  {getPaymentIcon(
                    option.id === BALANCE_PAYMENT_OPTION_ID
                      ? 'balance'
                      : undefined,
                    'size-4',
                    option.icon,
                    option.name
                  )}
                </span>
                <span className='truncate'>{option.name}</span>
              </span>
            ),
          },
          {
            id: 'status',
            header: t('Status'),
            className: 'w-24',
            cell: (option) => (
              <BadgeCell>
                <StatusBadge
                  label={option.enabled ? t('Enabled') : t('Disabled')}
                  variant={option.enabled ? 'success' : 'neutral'}
                  copyable={false}
                />
              </BadgeCell>
            ),
          },
          {
            id: 'channels',
            header: t('Channels'),
            className: 'w-32',
            cell: (option) => {
              if (option.id === BALANCE_PAYMENT_OPTION_ID) {
                return <span className='text-muted-foreground'>-</span>
              }
              if (option.methods === 0) {
                return (
                  <BadgeCell>
                    <StatusBadge
                      label={t('No channels')}
                      variant='warning'
                      copyable={false}
                    />
                  </BadgeCell>
                )
              }
              return formatNumber(option.methods, locale)
            },
          },
          {
            id: 'fee',
            header: t('Payment fee'),
            className: 'w-36',
            cellClassName: 'whitespace-nowrap',
            cell: (option) => {
              if (option.fee_mode === PAYMENT_FEE_MODE.NONE) {
                return (
                  <span className='text-muted-foreground'>{t('No fee')}</span>
                )
              }
              const parts: string[] = []
              if (usesFeeRate(option.fee_mode)) {
                parts.push(`${formatNumber(option.fee_rate_bp / 100, locale)}%`)
              }
              if (usesFixedFee(option.fee_mode)) {
                parts.push(
                  `${formatNumber(option.fee_fixed / 100, locale)} ${props.currency}`
                )
              }
              return parts.join(' + ')
            },
          },
          {
            id: 'sort',
            header: t('Sort'),
            className: 'w-16',
            cell: (option) => option.sort,
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'w-24 text-right',
            cellClassName: 'text-right',
            cell: (option) => (
              <div className='flex justify-end gap-1'>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => openDialog(option)}
                  aria-label={t('Edit')}
                >
                  <Pencil />
                </Button>
                <DataTableRowActionMenu ariaLabel={t('Open menu')}>
                  <DropdownMenuItem
                    disabled={saveOption.isPending}
                    onClick={() =>
                      saveOption.mutate({
                        id: option.id,
                        input: {
                          ...toPaymentOptionInput(option),
                          enabled: !option.enabled,
                        },
                      })
                    }
                  >
                    {option.enabled ? t('Disable') : t('Enable')}
                  </DropdownMenuItem>
                  {option.id !== BALANCE_PAYMENT_OPTION_ID && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        onClick={() => setDeleteTarget(option)}
                        className='text-destructive focus:text-destructive'
                      >
                        {t('Delete')}
                        <DropdownMenuShortcut>
                          <Trash2 size={16} />
                        </DropdownMenuShortcut>
                      </DropdownMenuItem>
                    </>
                  )}
                </DataTableRowActionMenu>
              </div>
            ),
          },
        ]}
      />

      <PaymentOptionDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        option={editing}
        currency={props.currency}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t('Delete payment method')}
        desc={t(
          'Delete "{{name}}"? Buyers can no longer choose it. Orders already paid with it keep their records.',
          { name: deleteTarget?.name ?? '' }
        )}
        confirmText={t('Delete')}
        destructive
        handleConfirm={() =>
          deleteTarget &&
          deleteOption.mutate(deleteTarget.id, {
            onSuccess: () => setDeleteTarget(null),
          })
        }
        isLoading={deleteOption.isPending}
      />
    </SettingsSection>
  )
}
