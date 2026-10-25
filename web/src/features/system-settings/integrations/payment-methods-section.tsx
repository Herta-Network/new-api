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
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import { SettingsSection } from '../components/settings-section'
import { PaymentMethodDialog } from './payment-method-dialog'
import {
  BALANCE_PAYMENT_PLATFORM,
  toPaymentMethodInput,
  useDeletePaymentMethod,
  usePaymentMethods,
  usePaymentOptions,
  usePaymentPlatforms,
  useSavePaymentMethod,
  useSyncPaymentWebhook,
  type PaymentMethod,
  type PaymentOption,
  type PaymentPlatform,
} from './payment-methods-api'
import { PaymentOptionsSection } from './payment-options-section'

// Stable empty lists: the channel dialog resets its form when they change.
const NO_PLATFORMS: PaymentPlatform[] = []
const NO_OPTIONS: PaymentOption[] = []

type PaymentMethodsSectionProps = {
  /** Site currency fees and amounts are counted in. */
  currency: string
}

/**
 * The payment methods buyers choose from, then the channels that collect
 * them: gateway accounts that take a method's orders within an amount range,
 * by priority and weight.
 */
export function PaymentMethodsSection(props: PaymentMethodsSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const optionsQuery = usePaymentOptions()
  const methodsQuery = usePaymentMethods()
  const platformsQuery = usePaymentPlatforms()
  const saveMethod = useSavePaymentMethod()
  const deleteMethod = useDeletePaymentMethod()
  const syncWebhook = useSyncPaymentWebhook()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<PaymentMethod | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<PaymentMethod | null>(null)
  const platforms = platformsQuery.data?.platforms ?? NO_PLATFORMS
  const options = optionsQuery.data ?? NO_OPTIONS
  // The balance channel follows the balance payment method and is not
  // edited.
  const channels = (methodsQuery.data ?? []).filter(
    (method) => method.platform !== BALANCE_PAYMENT_PLATFORM
  )
  const optionName = (id: number) =>
    options.find((option) => option.id === id)?.name ?? String(id)
  const amountRange = (method: PaymentMethod) => {
    const min = formatNumber(method.min_amount / 100, locale)
    const max = formatNumber(method.max_amount / 100, locale)
    if (method.min_amount === 0 && method.max_amount === 0) {
      return t('Any amount')
    }
    if (method.max_amount === 0) {
      return t('{{min}} {{currency}} and above', {
        min,
        currency: props.currency,
      })
    }
    if (method.min_amount === 0) {
      return t('Up to {{max}} {{currency}}', { max, currency: props.currency })
    }
    return t('{{min}} – {{max}} {{currency}}', {
      min,
      max,
      currency: props.currency,
    })
  }

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

  if (optionsQuery.isError || methodsQuery.isError || platformsQuery.isError) {
    return (
      <SettingsSection title={t('Payment Methods')}>
        <ErrorState
          title={t('Failed to load payment methods')}
          onRetry={() => {
            void optionsQuery.refetch()
            void methodsQuery.refetch()
            void platformsQuery.refetch()
          }}
        />
      </SettingsSection>
    )
  }

  return (
    <>
      <PaymentOptionsSection
        options={options}
        loading={optionsQuery.isLoading}
        currency={props.currency}
      />

      <SettingsSection title={t('Payment Channels')}>
        <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Channels collect the payment methods. An order goes to a channel that takes its amount, the lowest priority first; buyers never see which.'
            )}
          </p>
          <Button
            size='sm'
            onClick={() => openDialog(null)}
            disabled={platforms.length === 0}
          >
            <Plus data-icon='inline-start' />
            {t('Add payment channel')}
          </Button>
        </div>

        <StaticDataTable
          data={channels}
          getRowKey={(method) => method.id}
          tableClassName='min-w-[1040px] table-fixed'
          emptyClassName='text-sm'
          emptyContent={
            methodsQuery.isLoading
              ? t('Loading...')
              : t('No payment channels configured yet.')
          }
          columns={[
            {
              id: 'name',
              header: t('Name'),
              className: 'w-40',
              cell: (method) => (
                <span className='flex min-w-0 flex-col gap-0.5'>
                  <span className='truncate font-medium'>{method.name}</span>
                  <span className='text-muted-foreground truncate text-xs'>
                    {optionName(method.option_id)}
                  </span>
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
                        platforms.find(
                          (item) => item.platform === method.platform
                        )?.name ?? method.platform
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
              id: 'routing',
              header: t('Takes'),
              className: 'w-44',
              cell: (method) => (
                <span className='flex min-w-0 flex-col gap-0.5'>
                  <span className='truncate'>{amountRange(method)}</span>
                  <span className='text-muted-foreground text-xs'>
                    {t('Priority {{priority}} · weight {{weight}}', {
                      priority: method.priority,
                      weight: method.weight,
                    })}
                  </span>
                </span>
              ),
            },
            {
              id: 'status',
              header: t('Status'),
              className: 'w-36',
              cell: (method) => (
                <span className='flex min-w-0 flex-col items-start gap-1'>
                  <BadgeCell>
                    <StatusBadge
                      label={method.enabled ? t('Enabled') : t('Disabled')}
                      variant={method.enabled ? 'success' : 'neutral'}
                      copyable={false}
                    />
                  </BadgeCell>
                  <ChannelHealth method={method} />
                </span>
              ),
            },
            {
              id: 'notify-url',
              header: t('Callback URL'),
              cell: (method) => <PaymentMethodNotifyUrl method={method} />,
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
          options={options}
          currency={props.currency}
        />

        <ConfirmDialog
          open={deleteTarget !== null}
          onOpenChange={(open) => !open && setDeleteTarget(null)}
          title={t('Delete payment channel')}
          desc={t(
            'Delete "{{name}}"? Its payment method stops using it. Orders already paid through it keep their records.',
            { name: deleteTarget?.name ?? '' }
          )}
          confirmText={t('Delete')}
          destructive
          handleConfirm={handleDelete}
          isLoading={deleteMethod.isPending}
        />
      </SettingsSection>
    </>
  )
}

/**
 * Failed checkouts of a channel this server saw: a tripped channel is tried
 * after the others until its cooldown ends. The last error is in the badge's
 * title, which replaces the label StatusBadge would show there.
 */
function ChannelHealth(props: { method: PaymentMethod }) {
  const { t } = useTranslation()
  const health = props.method.health
  if (!health) return null
  const tripped = health.tripped_until * 1000 > Date.now()
  return (
    <BadgeCell>
      <StatusBadge
        label={
          tripped
            ? t('Tripped until {{time}}', {
                time: formatTimestampToDate(health.tripped_until),
              })
            : t('{{count}} failed checkouts', { count: health.failures })
        }
        variant={tripped ? 'danger' : 'warning'}
        copyable={false}
        title={health.last_error}
      />
    </BadgeCell>
  )
}

/**
 * The payment method type a Stripe channel collects, and an EPay channel's
 * payment page, which orders are never switched to; nothing for others.
 */
function StripePaymentType(props: { method: PaymentMethod }) {
  const { t } = useTranslation()
  if (
    props.method.platform === 'epay' &&
    props.method.config.checkout === 'page'
  ) {
    return (
      <span className='text-muted-foreground text-xs'>
        {t('Payment page, not switched to')}
      </span>
    )
  }
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
