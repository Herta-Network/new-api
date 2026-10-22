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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useMemo } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { PasswordInput } from '@/components/password-input'
import { StatusBadge, type StatusVariant } from '@/components/status-badge'
import { Accordion } from '@/components/ui/accordion'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatExchangeRate } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import { SettingsAccordion } from '../components/settings-accordion'
import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  currencySettingsSchema,
  parseRateOverrides,
  rateRowDirection,
  serializeRateOverrides,
  siteRateStatus,
  type CurrencySettingsValues,
} from './currency-settings-form'
import {
  exchangeRatesQueryKey,
  useExchangeRates,
  useRefreshExchangeRates,
  type ExchangeRatePair,
  type ExchangeRateUse,
} from './exchange-rates-api'
import { usePaymentPlatforms } from './payment-methods-api'

const RATE_USE_LABELS: Record<ExchangeRateUse, string> = {
  quota: 'Top-ups and displayed amounts',
  epay: 'EPay charges',
  channel_balance: 'Moonshot and DeepSeek balances',
}

const RATE_STATUS: Record<
  ExchangeRatePair['status'] | 'manual',
  { label: string; variant: StatusVariant }
> = {
  manual: { label: 'Overridden', variant: 'info' },
  live: { label: 'Live', variant: 'success' },
  stale: { label: 'Stale', variant: 'warning' },
  missing: { label: 'Missing', variant: 'danger' },
}

type CurrencySettingsSectionProps = {
  defaultValues: {
    currency: string
    quotaDisplayMode: string
    /** payment_setting.exchange_rate_overrides as stored (JSON). */
    overrides: string
    billingApiUsesTokenQuota: boolean
    exchangeRateKeyConfigured: boolean
    quotaPerUnit: number
  }
}

export function CurrencySettingsSection(props: CurrencySettingsSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()
  const platformsQuery = usePaymentPlatforms()
  const refreshRates = useRefreshExchangeRates()

  const initialValues = useMemo<CurrencySettingsValues>(
    () => ({
      currency: props.defaultValues.currency,
      quota_display_mode:
        props.defaultValues.quotaDisplayMode === 'tokens'
          ? 'tokens'
          : 'currency',
      exchange_rate_api_key: '',
      overrides: parseRateOverrides(props.defaultValues.overrides),
      billing_api_uses_token_quota:
        props.defaultValues.billingApiUsesTokenQuota,
    }),
    [props.defaultValues]
  )
  const form = useForm<CurrencySettingsValues>({
    resolver: zodResolver(currencySettingsSchema),
    defaultValues: initialValues,
  })
  const isSaving = form.formState.isSubmitting || updateOption.isPending
  useResetForm(form, isSaving ? undefined : initialValues)
  const currency = useWatch({ control: form.control, name: 'currency' })
  const overrides = useWatch({ control: form.control, name: 'overrides' })
  const ratesQuery = useExchangeRates(currency)

  const currencies = platformsQuery.data?.currencies ?? []
  const currencyOptions = currencies.includes(currency)
    ? currencies
    : [currency, ...currencies]
  const pairs = ratesQuery.data?.pairs ?? []
  const keyConfigured = props.defaultValues.exchangeRateKeyConfigured

  const onSubmit = async (values: CurrencySettingsValues) => {
    const updates: Array<{ key: string; value: string }> = []
    if (values.currency !== initialValues.currency) {
      updates.push({ key: 'payment_setting.currency', value: values.currency })
    }
    if (values.quota_display_mode !== initialValues.quota_display_mode) {
      updates.push({
        key: 'general_setting.quota_display_mode',
        value: values.quota_display_mode,
      })
    }
    // A blank key keeps the stored one.
    const apiKey = values.exchange_rate_api_key.trim()
    if (apiKey) {
      updates.push({
        key: 'payment_setting.exchange_rate_api_key',
        value: apiKey,
      })
    }
    const savedOverrides = serializeRateOverrides(values.overrides)
    if (savedOverrides !== serializeRateOverrides(initialValues.overrides)) {
      updates.push({
        key: 'payment_setting.exchange_rate_overrides',
        value: savedOverrides,
      })
    }
    if (
      values.billing_api_uses_token_quota !==
      initialValues.billing_api_uses_token_quota
    ) {
      updates.push({
        key: 'general_setting.billing_api_uses_token_quota',
        value: String(values.billing_api_uses_token_quota),
      })
    }

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    try {
      for (const update of updates) {
        await updateOption.mutateAsync(update)
      }
      form.setValue('exchange_rate_api_key', '')
    } catch (error) {
      handleServerError(error)
      return
    } finally {
      void queryClient.invalidateQueries({ queryKey: exchangeRatesQueryKey })
    }
    // A new site currency or key needs live rates now, not at the next
    // daily refresh.
    const needsLiveRates =
      values.currency !== initialValues.currency || apiKey !== ''
    if (needsLiveRates && (keyConfigured || apiKey !== '')) {
      refreshRates.mutate()
    }
  }

  const siteRate = siteRateStatus(currency, ratesQuery.data?.pairs, overrides)
  let siteRateText = t('Loading...')
  switch (siteRate.state) {
    case 'usd':
      siteRateText = t('Amounts are shown in USD; they need no exchange rate.')
      break
    case 'manual':
      siteRateText = t(
        'Amounts are shown in {{currency}} at the rate you set: 1 USD = {{rate}} {{currency}}.',
        { currency, rate: formatExchangeRate(siteRate.usdRate, locale) }
      )
      break
    case 'live':
      siteRateText = t(
        'Amounts are shown in {{currency}} at the live rate 1 USD = {{rate}} {{currency}}, fetched {{time}}.',
        {
          currency,
          rate: formatExchangeRate(siteRate.usdRate, locale),
          time: formatTimestampToDate(siteRate.fetchedAt),
        }
      )
      break
    case 'missing':
      siteRateText = t(
        'There is no usable rate for {{currency}}: amounts are shown in USD and top-ups are refused until you set a rate or a live rate is fetched.',
        { currency }
      )
      break
  }

  return (
    <SettingsSection title={t('Currency & Exchange Rates')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={isSaving}
          />

          <FormField
            control={form.control}
            name='currency'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Site currency')}</FormLabel>
                <Select
                  items={currencyOptions.map((code) => ({
                    value: code,
                    label: code,
                  }))}
                  value={field.value}
                  onValueChange={(value) => field.onChange(String(value ?? ''))}
                >
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {currencyOptions.map((code) => (
                        <SelectItem key={code} value={code}>
                          {code}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Prices, top-ups and every amount users and administrators see are in this currency. Changing it does not convert existing prices.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='quota_display_mode'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Display Mode')}</FormLabel>
                <Select
                  items={[
                    {
                      value: 'currency',
                      label: t('Amounts in {{currency}}', { currency }),
                    },
                    { value: 'tokens', label: t('Tokens Only') },
                  ]}
                  value={field.value}
                  onValueChange={field.onChange}
                >
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value='currency'>
                        {t('Amounts in {{currency}}', { currency })}
                      </SelectItem>
                      <SelectItem value='tokens'>{t('Tokens Only')}</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Tokens Only shows balances and usage as raw quota; prices stay in the site currency.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='exchange_rate_api_key'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='flex items-center gap-2'>
                  {t('Exchange rate API key')}
                  <StatusBadge
                    label={
                      keyConfigured ? t('Configured') : t('Not configured')
                    }
                    variant={keyConfigured ? 'success' : 'neutral'}
                    copyable={false}
                  />
                </FormLabel>
                <FormControl>
                  <PasswordInput
                    {...field}
                    autoComplete='new-password'
                    placeholder={
                      keyConfigured ? t('Enter new key to update') : undefined
                    }
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'An apilayer currency data key for live rates, refreshed daily. Without one, set the rates below by hand. Leave blank to keep the existing key.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <div data-settings-form-span='full' className='min-w-0 space-y-3'>
            <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
              <div className='space-y-0.5'>
                <h3 className='text-sm font-medium'>{t('Exchange rates')}</h3>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Payments and displayed amounts convert with the same rate: the one you set for a pair, else the live rate. A rate you set applies to both directions of its pair, and the pair is no longer fetched.'
                  )}
                </p>
              </div>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={!keyConfigured || refreshRates.isPending}
                onClick={() => refreshRates.mutate()}
              >
                <RefreshCw data-icon='inline-start' aria-hidden='true' />
                {t('Refresh live rates')}
              </Button>
            </div>

            <Alert
              variant={siteRate.state === 'missing' ? 'destructive' : 'default'}
            >
              <AlertDescription>{siteRateText}</AlertDescription>
            </Alert>

            <StaticDataTable
              data={pairs}
              getRowKey={(pair) => `${pair.base}/${pair.quote}`}
              tableClassName='min-w-[760px] table-fixed'
              emptyClassName='text-sm'
              emptyContent={
                ratesQuery.isLoading
                  ? t('Loading...')
                  : t('This site needs no exchange rate.')
              }
              columns={[
                {
                  id: 'pair',
                  header: t('Currency pair'),
                  className: 'w-36',
                  cell: (pair) => {
                    const direction = rateRowDirection(pair, overrides)
                    return (
                      <span className='font-mono text-sm'>
                        1 {direction.from} = ? {direction.to}
                      </span>
                    )
                  },
                },
                {
                  id: 'uses',
                  header: t('Used for'),
                  cell: (pair) => (
                    <span className='text-sm'>
                      {pair.uses.length > 0
                        ? pair.uses
                            .map((use) => t(RATE_USE_LABELS[use]))
                            .join(', ')
                        : t('Not used')}
                    </span>
                  ),
                },
                {
                  id: 'live',
                  header: t('Live rate'),
                  className: 'w-44',
                  cell: (pair) => {
                    if (!pair.live) {
                      return <span className='text-muted-foreground'>-</span>
                    }
                    const direction = rateRowDirection(pair, overrides)
                    const rate = direction.reversed
                      ? 1 / Number(pair.live.rate)
                      : pair.live.rate
                    return (
                      <div className='space-y-0.5'>
                        <div className='font-mono text-sm tabular-nums'>
                          {formatExchangeRate(rate, locale)}
                        </div>
                        <div className='text-muted-foreground text-xs'>
                          {t('Fetched {{time}}', {
                            time: formatTimestampToDate(pair.live.fetched_at),
                          })}
                        </div>
                      </div>
                    )
                  },
                },
                {
                  id: 'override',
                  header: t('Rate you set'),
                  className: 'w-44',
                  cell: (pair) => {
                    const direction = rateRowDirection(pair, overrides)
                    return (
                      <FormField
                        control={form.control}
                        name={`overrides.${direction.key}`}
                        render={({ field }) => (
                          <FormItem>
                            <FormControl>
                              <Input
                                {...field}
                                value={field.value ?? ''}
                                inputMode='decimal'
                                aria-label={t(
                                  'Rate you set for {{from}} to {{to}}',
                                  { from: direction.from, to: direction.to }
                                )}
                                placeholder={t('Use the live rate')}
                              />
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    )
                  },
                },
                {
                  id: 'status',
                  header: t('Status'),
                  className: 'w-28',
                  cell: (pair) => {
                    const direction = rateRowDirection(pair, overrides)
                    const status = overrides[direction.key]?.trim()
                      ? RATE_STATUS.manual
                      : RATE_STATUS[pair.status]
                    return (
                      <StatusBadge
                        label={t(status.label)}
                        variant={status.variant}
                        copyable={false}
                      />
                    )
                  },
                },
              ]}
            />
          </div>

          <FormField
            control={form.control}
            name='billing_api_uses_token_quota'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t("Billing API reports the API key's quota")}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      "The OpenAI-compatible /dashboard/billing endpoints report the calling API key's quota. When off, they report the user's wallet."
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <Accordion data-settings-form-span='full'>
            <SettingsAccordion value='advanced' title={t('Advanced')}>
              <div className='space-y-2'>
                <Label htmlFor='quota-per-unit'>
                  {t('Quota units per USD')}
                </Label>
                <Input
                  id='quota-per-unit'
                  aria-describedby='quota-per-unit-description'
                  value={props.defaultValues.quotaPerUnit}
                  readOnly
                  disabled
                />
                <p
                  id='quota-per-unit-description'
                  className='text-muted-foreground text-sm'
                >
                  {t(
                    'The internal unit quota is counted in. It is fixed; amounts convert from it to USD, then to the site currency.'
                  )}
                </p>
              </div>
            </SettingsAccordion>
          </Accordion>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
