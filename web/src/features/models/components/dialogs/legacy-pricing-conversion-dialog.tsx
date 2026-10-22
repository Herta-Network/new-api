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
import { isAxiosError } from 'axios'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  invalidateModelPricing,
  previewLegacyPricingConversions,
  saveModelPricing,
  type LegacyPricingConversion,
} from '@/features/model-pricing/api'
import { USD_PRICING_CURRENCY } from '@/features/model-pricing/currency'
import { legacyPriceRows } from '@/features/model-pricing/pricing-mode'
import { handleServerError } from '@/lib/handle-server-error'

function CurrentPrices(props: { entry: LegacyPricingConversion }) {
  const { t } = useTranslation()
  const before = legacyPriceRows(
    {
      modelName: props.entry.model_name,
      effective: props.entry.effective ?? {},
      cacheWriteMode: props.entry.cache_write_mode,
      billingDetails: props.entry.billing_details,
    },
    t,
    USD_PRICING_CURRENCY
  )
  return (
    <dl className='space-y-0.5 text-xs'>
      {before.rows.map((row) => (
        <div key={row.key} className='flex flex-wrap gap-x-1.5'>
          <dt className='text-muted-foreground'>{row.label}</dt>
          <dd className='font-mono tabular-nums'>
            {row.value}
            {row.unit === undefined && !before.perRequest && ' / 1M'}
          </dd>
        </div>
      ))}
    </dl>
  )
}

/**
 * Converts every legacy ratio and per-request price in one save. The server
 * previews each model with the single-model conversion; the save is one
 * versioned batch, so a price edited after the preview rejects all of it.
 */
export function LegacyPricingConversionDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [conflict, setConflict] = useState(false)
  const preview = useQuery({
    queryKey: ['model-pricing-legacy-conversion'],
    queryFn: previewLegacyPricingConversions,
    enabled: props.open,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
    retry: false,
  })
  const queryClient = useQueryClient()
  // A stale version is shown inline with a reload, not as a toast.
  const save = useMutation({
    mutationFn: saveModelPricing,
    meta: { errorToast: false },
    onSuccess: () => invalidateModelPricing(queryClient),
  })
  const entries = preview.data ?? []
  const convertible = entries.filter((entry) => entry.expression)
  const manual = entries.length - convertible.length

  const reload = () => {
    setConflict(false)
    void preview.refetch()
  }
  const convert = async () => {
    setConflict(false)
    try {
      await save.mutateAsync(
        convertible.map((entry) => ({
          model_name: entry.model_name,
          expected_version: entry.version,
          // Legacy fields stay saved, as with a single conversion, so the
          // previous mode can still be restored.
          pricing: {
            ...entry.configured,
            'billing_setting.billing_mode': 'tiered_expr',
            'billing_setting.billing_expr': entry.expression,
          },
        }))
      )
      toast.success(
        t('Models converted to expression pricing: {{count}}', {
          count: convertible.length,
        })
      )
      props.onOpenChange(false)
    } catch (error) {
      if (isAxiosError(error) && error.response?.status === 409) {
        setConflict(true)
        return
      }
      handleServerError(error, t('Failed to save model pricing'))
    }
  }

  let content = <LoadingState />
  if (preview.isError) {
    content = (
      <ErrorState description={preview.error.message} onRetry={reload} />
    )
  } else if (preview.data && entries.length === 0) {
    content = (
      <EmptyState
        title={t('No models use legacy pricing')}
        description={t(
          'Every priced model in this workspace already uses expression pricing.'
        )}
      />
    )
  } else if (preview.data) {
    content = (
      <div className='space-y-4'>
        <p className='text-sm' role='status'>
          {t('To convert: {{count}}', { count: convertible.length })}
          {manual > 0 &&
            ` · ${t('Needs manual conversion: {{count}}', { count: manual })}`}
        </p>
        <StaticDataTable
          data={entries}
          getRowKey={(entry) => entry.model_name}
          columns={[
            {
              id: 'model',
              header: t('Model'),
              cellClassName: 'align-top font-mono text-sm break-all',
              cell: (entry) => entry.model_name,
            },
            {
              id: 'current',
              header: t('Current prices'),
              cellClassName: 'align-top',
              cell: (entry) => <CurrentPrices entry={entry} />,
            },
            {
              id: 'expression',
              header: t('Billing expression'),
              cellClassName: 'align-top whitespace-normal',
              cell: (entry) =>
                entry.expression ? (
                  <code className='text-xs break-all'>{entry.expression}</code>
                ) : (
                  <span className='text-warning text-xs'>
                    {t(entry.unsupported_reason ?? '')}
                  </span>
                ),
            },
          ]}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'After conversion, expression reservation and rounding rules apply. Effective unit prices are preserved; individual rounded charges may differ.'
          )}
        </p>
      </div>
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!save.isPending) props.onOpenChange(open)
      }}
      title={t('Convert legacy prices')}
      description={t(
        'Rewrites legacy ratio and per-request prices of the models in this workspace as billing expressions with the same unit prices. Prices are in USD.'
      )}
      contentClassName='sm:max-w-5xl'
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={save.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={() => void convert()}
            disabled={
              save.isPending ||
              preview.isFetching ||
              conflict ||
              convertible.length === 0
            }
          >
            {save.isPending
              ? t('Saving...')
              : t('Convert ({{count}})', { count: convertible.length })}
          </Button>
        </>
      }
    >
      {conflict && (
        <Alert variant='destructive' className='mb-4'>
          <AlertDescription className='flex flex-wrap items-center justify-between gap-2'>
            <span>
              {t(
                'Some prices changed after this preview, so nothing was saved. Reload the preview and convert again.'
              )}
            </span>
            <Button size='sm' variant='outline' onClick={reload}>
              {t('Reload preview')}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      {content}
    </Dialog>
  )
}
