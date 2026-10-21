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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Code2, Eye, ShieldAlert } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { PasswordInput } from '@/components/password-input'
import { RiskAcknowledgementDialog } from '@/components/risk-acknowledgement-dialog'
import { StatusBadge } from '@/components/status-badge'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { cn } from '@/lib/utils'

import { confirmPaymentCompliance } from '../api'
import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { AmountDiscountVisualEditor } from './amount-discount-visual-editor'
import { AmountOptionsVisualEditor } from './amount-options-visual-editor'
import { usePaymentPlatforms } from './payment-methods-api'
import {
  formatJsonForEditor,
  getJsonError,
  normalizeJsonForComparison,
} from './utils'

const CURRENT_COMPLIANCE_TERMS_VERSION = 'v1'

const paymentSchema = z.object({
  currency: z.string().min(1),
  min_topup: z
    .string()
    .trim()
    .refine(
      (value) => /^\d+(\.\d{1,2})?$/.test(value) && Number(value) > 0,
      'The minimum top-up must be a positive amount with at most two decimal places'
    ),
  amount_options: z.string().superRefine((value, ctx) => {
    const error = getJsonError(value, (parsed) => Array.isArray(parsed))
    if (error) ctx.addIssue({ code: 'custom', message: error })
  }),
  amount_discount: z.string().superRefine((value, ctx) => {
    const error = getJsonError(
      value,
      (parsed) =>
        !!parsed && typeof parsed === 'object' && !Array.isArray(parsed)
    )
    if (error) ctx.addIssue({ code: 'custom', message: error })
  }),
  exchange_rate_api_key: z.string(),
})

type PaymentFormValues = z.infer<typeof paymentSchema>

type PaymentSettingsSectionProps = {
  defaultValues: {
    currency: string
    minTopup: string
    amountOptions: string
    amountDiscount: string
    exchangeRateKeyConfigured: boolean
  }
  complianceDefaults: {
    confirmed: boolean
    termsVersion: string
    confirmedAt: number
    confirmedBy: number
  }
}

export function PaymentSettingsSection(props: PaymentSettingsSectionProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const updateOption = useUpdateOption()
  const platformsQuery = usePaymentPlatforms()
  const [amountOptionsVisual, setAmountOptionsVisual] = useState(true)
  const [amountDiscountVisual, setAmountDiscountVisual] = useState(true)
  const [showComplianceDialog, setShowComplianceDialog] = useState(false)

  const initialValues = useMemo<PaymentFormValues>(
    () => ({
      currency: props.defaultValues.currency,
      min_topup: props.defaultValues.minTopup,
      amount_options: formatJsonForEditor(props.defaultValues.amountOptions),
      amount_discount: formatJsonForEditor(props.defaultValues.amountDiscount),
      exchange_rate_api_key: '',
    }),
    [props.defaultValues]
  )
  const form = useForm<PaymentFormValues>({
    resolver: zodResolver(paymentSchema),
    defaultValues: initialValues,
  })
  const isSaving = form.formState.isSubmitting || updateOption.isPending
  useResetForm(form, isSaving ? undefined : initialValues)
  const currency = useWatch({ control: form.control, name: 'currency' })

  const currencies = platformsQuery.data?.currencies ?? []
  const currencyOptions = currencies.includes(currency)
    ? currencies
    : [currency, ...currencies]

  const complianceStatements = [
    t(
      'You have legally obtained authorization for the connected model APIs, accounts, keys, and quotas.'
    ),
    t(
      'You commit to using upstream APIs, accounts, keys, quotas, and service capabilities only within the scope of lawful authorization obtained from upstream service providers, model service providers, or relevant rights holders, and will not conduct unauthorized resale, trafficking, distribution, or other non-compliant commercialization.'
    ),
    t(
      'If you provide generative AI services to the public in mainland China, you will fulfill legal obligations including filing, security assessment, content safety, complaint handling, generated content labeling, log retention, and personal information protection.'
    ),
    t(
      'You commit not to use this system to implement, assist with, or indirectly implement acts that violate applicable laws and regulations, regulatory requirements, platform rules, public interests, or the lawful rights and interests of third parties.'
    ),
    t(
      'You understand and independently bear legal responsibility arising from deployment, operation, and charging behavior.'
    ),
    t(
      'You understand this compliance reminder is only for risk notice and does not constitute legal advice, a compliance review conclusion, or a guarantee of the legality of your use of this system; you should consult professional legal or compliance advisors based on your actual business scenario.'
    ),
  ]

  const complianceConfirmed =
    props.complianceDefaults.confirmed &&
    props.complianceDefaults.termsVersion === CURRENT_COMPLIANCE_TERMS_VERSION

  const confirmCompliance = useMutation({
    mutationFn: confirmPaymentCompliance,
    onSuccess: (data) => {
      if (!data.success) {
        handleServerError(data, t('Failed to confirm compliance'))
        return
      }
      toast.success(t('Compliance confirmed successfully'))
      setShowComplianceDialog(false)
      void queryClient.invalidateQueries({ queryKey: ['system-options'] })
    },
    onError: (error: Error) => {
      handleServerError(error, t('Failed to confirm compliance'))
    },
  })

  const onSubmit = async (values: PaymentFormValues) => {
    const updates: Array<{ key: string; value: string }> = []
    if (values.currency !== initialValues.currency) {
      updates.push({ key: 'payment_setting.currency', value: values.currency })
    }
    const minTopup = values.min_topup.trim()
    if (minTopup !== initialValues.min_topup.trim()) {
      updates.push({ key: 'payment_setting.min_topup', value: minTopup })
    }
    if (
      normalizeJsonForComparison(values.amount_options) !==
      normalizeJsonForComparison(initialValues.amount_options)
    ) {
      updates.push({
        key: 'payment_setting.amount_options',
        value: values.amount_options.trim(),
      })
    }
    if (
      normalizeJsonForComparison(values.amount_discount) !==
      normalizeJsonForComparison(initialValues.amount_discount)
    ) {
      updates.push({
        key: 'payment_setting.amount_discount',
        value: values.amount_discount.trim(),
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
    }
  }

  return (
    <SettingsSection title={t('Payment Settings')}>
      {complianceConfirmed ? (
        <Alert>
          <AlertTitle>{t('Compliance confirmed')}</AlertTitle>
          <AlertDescription>
            {t('Confirmed at {{time}} by user #{{userId}}', {
              time: props.complianceDefaults.confirmedAt
                ? formatTimestampToDate(props.complianceDefaults.confirmedAt)
                : '-',
              userId: props.complianceDefaults.confirmedBy || '-',
            })}
          </AlertDescription>
        </Alert>
      ) : (
        <Alert variant='destructive'>
          <ShieldAlert aria-hidden='true' />
          <AlertTitle>{t('Compliance confirmation required')}</AlertTitle>
          <AlertDescription>
            <div className='space-y-3'>
              <p>
                {t(
                  'Payment, redemption codes, subscription plans, and invitation rewards are locked until the root administrator confirms the compliance terms.'
                )}
              </p>
              <ol className='list-decimal space-y-1 pl-5'>
                {complianceStatements.map((statement) => (
                  <li key={statement}>{statement}</li>
                ))}
              </ol>
            </div>
          </AlertDescription>
          <AlertAction>
            <Button
              type='button'
              size='sm'
              variant='destructive'
              onClick={() => setShowComplianceDialog(true)}
            >
              {t('Confirm compliance')}
            </Button>
          </AlertAction>
        </Alert>
      )}

      <RiskAcknowledgementDialog
        open={showComplianceDialog}
        onOpenChange={setShowComplianceDialog}
        title={t('Confirm compliance terms')}
        description={t(
          'This confirmation unlocks payment, redemption code, subscription plan, and invitation reward features. Please read the statements carefully.'
        )}
        items={complianceStatements}
        requiredText={t(
          'I have read and understood the above compliance reminder, acknowledge the related legal risks, and confirm that I bear legal responsibility arising from deployment, operation, and charging behavior.'
        )}
        requiredTextParts={[
          {
            type: 'input',
            text: t('I have read and understood the above compliance reminder'),
          },
          { type: 'static', text: t('，') },
          { type: 'input', text: t('acknowledge the related legal risks') },
          { type: 'static', text: t('，and ') },
          {
            type: 'input',
            text: t(
              'confirm that I bear legal responsibility arising from deployment'
            ),
          },
          { type: 'static', text: t('、') },
          { type: 'input', text: t('operation and charging behavior') },
        ]}
        inputPrompt={t('Please type the following text to confirm:')}
        inputPlaceholder={t('Type the confirmation text here')}
        mismatchHint={t('The entered text does not match the required text.')}
        confirmText={t('Confirm and enable')}
        isLoading={confirmCompliance.isPending}
        onConfirm={() => confirmCompliance.mutate()}
      />

      <Form {...form}>
        <SettingsForm
          onSubmit={form.handleSubmit(onSubmit)}
          autoComplete='off'
          className={cn(
            !complianceConfirmed && 'pointer-events-none opacity-40'
          )}
        >
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
                    'Prices, top-up amounts and fees are in this currency. Changing it does not convert existing prices.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='min_topup'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('Minimum top-up ({{currency}})', { currency })}
                </FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    inputMode='decimal'
                    min={0}
                    step={0.01}
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t('The smallest amount a user can top up at once')}
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
                      props.defaultValues.exchangeRateKeyConfigured
                        ? t('Configured')
                        : t('Not configured')
                    }
                    variant={
                      props.defaultValues.exchangeRateKeyConfigured
                        ? 'success'
                        : 'neutral'
                    }
                    copyable={false}
                  />
                </FormLabel>
                <FormControl>
                  <PasswordInput
                    {...field}
                    autoComplete='new-password'
                    placeholder={
                      props.defaultValues.exchangeRateKeyConfigured
                        ? t('Enter new key to update')
                        : undefined
                    }
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'An apilayer currency data key. Needed when the site currency is not USD, or for EPay when it is not CNY. Leave blank to keep the existing key.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='amount_options'
            render={({ field }) => (
              <FormItem data-settings-form-span='full'>
                <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
                  <FormLabel>{t('Top-up amount options')}</FormLabel>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() => setAmountOptionsVisual(!amountOptionsVisual)}
                  >
                    {amountOptionsVisual ? (
                      <Code2 data-icon='inline-start' />
                    ) : (
                      <Eye data-icon='inline-start' />
                    )}
                    {amountOptionsVisual
                      ? t('JSON Editor')
                      : t('Visual Editor')}
                  </Button>
                </div>
                <FormControl>
                  {amountOptionsVisual ? (
                    <AmountOptionsVisualEditor
                      value={field.value}
                      onChange={field.onChange}
                      currency={currency}
                    />
                  ) : (
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      placeholder='[10, 20, 50, 100]'
                      heightClassName='h-40 min-h-40 max-h-40'
                      aria-invalid={Boolean(
                        form.formState.errors.amount_options
                      )}
                    />
                  )}
                </FormControl>
                <FormDescription>
                  {t('Preset recharge amounts (JSON array)')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='amount_discount'
            render={({ field }) => (
              <FormItem data-settings-form-span='full'>
                <div className='flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between'>
                  <FormLabel>{t('Amount discount')}</FormLabel>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() =>
                      setAmountDiscountVisual(!amountDiscountVisual)
                    }
                  >
                    {amountDiscountVisual ? (
                      <Code2 data-icon='inline-start' />
                    ) : (
                      <Eye data-icon='inline-start' />
                    )}
                    {amountDiscountVisual
                      ? t('JSON Editor')
                      : t('Visual Editor')}
                  </Button>
                </div>
                <FormControl>
                  {amountDiscountVisual ? (
                    <AmountDiscountVisualEditor
                      value={field.value}
                      onChange={field.onChange}
                      currency={currency}
                    />
                  ) : (
                    <JsonCodeEditor
                      value={field.value}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      textareaRef={field.ref}
                      placeholder='{"100":0.95,"200":0.9}'
                      heightClassName='h-40 min-h-40 max-h-40'
                      aria-invalid={Boolean(
                        form.formState.errors.amount_discount
                      )}
                    />
                  )}
                </FormControl>
                <FormDescription>
                  {t('Discount map by recharge amount (JSON object)')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
