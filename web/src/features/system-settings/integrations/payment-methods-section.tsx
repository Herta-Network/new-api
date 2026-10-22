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
import { Pencil, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { BadgeCell } from '@/components/data-table/core/badge-cell'
import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { ErrorState } from '@/components/error-state'
import { ReactIconByName } from '@/components/react-icon-by-name'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import { STRIPE_PAYMENT_METHOD_TYPE_LABELS } from '@/features/wallet/constants'
import { getPaymentIcon } from '@/features/wallet/lib/ui'
import type { StripePaymentMethodType } from '@/features/wallet/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { SettingsSection } from '../components/settings-section'
import { PaymentMethodDialog } from './payment-method-dialog'
import { usesFeeRate, usesFixedFee } from './payment-method-form'
import {
  BALANCE_PAYMENT_METHOD_ID,
  PAYMENT_FEE_MODE,
  toPaymentMethodInput,
  useDeletePaymentMethod,
  usePaymentMethods,
  usePaymentPlatforms,
  useSavePaymentMethod,
  useSyncPaymentWebhook,
  type PaymentMethod,
  type PaymentPlatform,
} from './payment-methods-api'

// A stable empty list: the method dialog resets its form when this changes.
const NO_PLATFORMS: PaymentPlatform[] = []

type PaymentMethodsSectionProps = {
  /** Site currency fixed fees are counted in. */
  currency: string
}

export function PaymentMethodsSection(props: PaymentMethodsSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const methodsQuery = usePaymentMethods()
  const platformsQuery = usePaymentPlatforms()
  const saveMethod = useSavePaymentMethod()
  const deleteMethod = useDeletePaymentMethod()
  const syncWebhook = useSyncPaymentWebhook()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<PaymentMethod | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<PaymentMethod | null>(null)
  const platforms = platformsQuery.data?.platforms ?? NO_PLATFORMS

  const openDialog = (method: PaymentMethod | null) => {
    setEditing(method)
    setDialogOpen(true)
  }

  const handleDelete = () => {
    if (!deleteTarget) return
    deleteMethod.mutate(deleteTarget.id, {
      onSuccess: () => setDeleteTarget(null),
    })
  }

  if (methodsQuery.isError || platformsQuery.isError) {
    return (
      <SettingsSection title={t('Payment Methods')}>
        <ErrorState
          title={t('Failed to load payment methods')}
          onRetry={() => {
            void methodsQuery.refetch()
            void platformsQuery.refetch()
          }}
        />
      </SettingsSection>
    )
  }

  return (
    <SettingsSection title={t('Payment Methods')}>
      <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Buyers choose from the enabled methods. The balance can pay for subscriptions only.'
          )}
        </p>
        <Button
          size='sm'
          onClick={() => openDialog(null)}
          disabled={platforms.length === 0}
        >
          <Plus data-icon='inline-start' />
          {t('Add payment method')}
        </Button>
      </div>

      <StaticDataTable
        data={methodsQuery.data ?? []}
        getRowKey={(method) => method.id}
        tableClassName='min-w-[880px] table-fixed'
        emptyClassName='text-sm'
        emptyContent={
          methodsQuery.isLoading
            ? t('Loading...')
            : t('No payment methods configured yet.')
        }
        columns={[
          {
            id: 'name',
            header: t('Name'),
            className: 'w-40',
            cell: (method) => (
              <span className='flex items-center gap-2 font-medium'>
                {method.icon && (
                  <ReactIconByName
                    name={method.icon}
                    className='text-muted-foreground size-4 shrink-0'
                    aria-hidden='true'
                  />
                )}
                <span className='truncate'>{method.name}</span>
              </span>
            ),
          },
          {
            id: 'platform',
            header: t('Platform'),
            className: 'w-32',
            cell: (method) => (
              <span className='flex min-w-0 flex-col items-start gap-1'>
                <BadgeCell>
                  <StatusBadge
                    label={
                      method.id === BALANCE_PAYMENT_METHOD_ID
                        ? t('Balance')
                        : (platforms.find(
                            (item) => item.platform === method.platform
                          )?.name ?? method.platform)
                    }
                    variant='neutral'
                    copyable={false}
                  />
                </BadgeCell>
                <StripePaymentType method={method} />
              </span>
            ),
          },
          {
            id: 'status',
            header: t('Status'),
            className: 'w-24',
            cell: (method) => (
              <BadgeCell>
                <StatusBadge
                  label={method.enabled ? t('Enabled') : t('Disabled')}
                  variant={method.enabled ? 'success' : 'neutral'}
                  copyable={false}
                />
              </BadgeCell>
            ),
          },
          {
            id: 'fee',
            header: t('Payment fee'),
            className: 'w-36',
            cellClassName: 'whitespace-nowrap',
            cell: (method) => {
              if (method.fee_mode === PAYMENT_FEE_MODE.NONE) {
                return (
                  <span className='text-muted-foreground'>{t('No fee')}</span>
                )
              }
              const parts: string[] = []
              if (usesFeeRate(method.fee_mode)) {
                parts.push(`${formatNumber(method.fee_rate_bp / 100, locale)}%`)
              }
              if (usesFixedFee(method.fee_mode)) {
                parts.push(
                  `${formatNumber(method.fee_fixed / 100, locale)} ${props.currency}`
                )
              }
              return parts.join(' + ')
            },
          },
          {
            id: 'notify-url',
            header: t('Callback URL'),
            cell: (method) => <PaymentMethodNotifyUrl method={method} />,
          },
          {
            id: 'sort',
            header: t('Sort'),
            className: 'w-16',
            cell: (method) => method.sort,
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'w-24 text-right',
            cellClassName: 'text-right',
            cell: (method) => (
              <div className='flex justify-end gap-1'>
                <Button
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => openDialog(method)}
                  aria-label={t('Edit')}
                  disabled={platformsQuery.isLoading}
                >
                  <Pencil />
                </Button>
                <DataTableRowActionMenu ariaLabel={t('Open menu')}>
                  <DropdownMenuItem
                    disabled={saveMethod.isPending}
                    onClick={() =>
                      saveMethod.mutate({
                        id: method.id,
                        input: {
                          ...toPaymentMethodInput(method),
                          enabled: !method.enabled,
                        },
                      })
                    }
                  >
                    {method.enabled ? t('Disable') : t('Enable')}
                  </DropdownMenuItem>
                  {method.platform === 'stripe' && (
                    <DropdownMenuItem
                      disabled={syncWebhook.isPending}
                      onClick={() => syncWebhook.mutate(method.id)}
                    >
                      {t('Sync webhook')}
                      <DropdownMenuShortcut>
                        <RefreshCw size={16} />
                      </DropdownMenuShortcut>
                    </DropdownMenuItem>
                  )}
                  {method.id !== BALANCE_PAYMENT_METHOD_ID && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        onClick={() => setDeleteTarget(method)}
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

      <PaymentMethodDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        method={editing}
        platforms={platforms}
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
        handleConfirm={handleDelete}
        isLoading={deleteMethod.isPending}
      />
    </SettingsSection>
  )
}

/** The payment method type a Stripe method collects; nothing for others. */
function StripePaymentType(props: { method: PaymentMethod }) {
  const { t } = useTranslation()
  const paymentType = props.method.config.payment_method_type
  if (
    props.method.platform !== 'stripe' ||
    typeof paymentType !== 'string' ||
    !(paymentType in STRIPE_PAYMENT_METHOD_TYPE_LABELS)
  ) {
    return null
  }
  const stripeType = paymentType as StripePaymentMethodType
  return (
    <span className='text-muted-foreground flex items-center gap-1.5 text-xs'>
      <span aria-hidden='true' className='flex'>
        {getPaymentIcon('stripe', 'size-3.5', '', '', stripeType)}
      </span>
      {t(STRIPE_PAYMENT_METHOD_TYPE_LABELS[stripeType])}
    </span>
  )
}

function PaymentMethodNotifyUrl(props: { method: PaymentMethod }) {
  const { t } = useTranslation()
  if (props.method.id === BALANCE_PAYMENT_METHOD_ID) {
    return <span className='text-muted-foreground'>-</span>
  }
  if (!props.method.notify_url) {
    return (
      <span className='text-muted-foreground text-sm'>
        {t('Set a public server address or callback domain')}
      </span>
    )
  }
  return (
    <span className='flex min-w-0 flex-col items-start gap-1'>
      <span className='flex max-w-full min-w-0 items-center gap-1.5'>
        <code
          className='bg-muted min-w-0 truncate rounded px-1.5 py-0.5 text-xs'
          title={props.method.notify_url}
        >
          {props.method.notify_url}
        </code>
        <CopyButton
          value={props.method.notify_url}
          size='icon'
          className='size-7 shrink-0'
          tooltip={t('Copy callback URL')}
          aria-label={t('Copy callback URL')}
        />
      </span>
      {props.method.webhook_stale && (
        <StatusBadge
          label={t('Webhook outdated')}
          variant='warning'
          copyable={false}
        />
      )}
    </span>
  )
}
