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
import { Code2, Eye } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { JsonCodeEditor } from '@/components/json-code-editor'
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
import { handleServerError } from '@/lib/handle-server-error'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import { AmountDiscountVisualEditor } from './amount-discount-visual-editor'
import { AmountOptionsVisualEditor } from './amount-options-visual-editor'
import {
  formatJsonForEditor,
  getJsonError,
  normalizeJsonForComparison,
} from './utils'

const paymentSchema = z.object({
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
})

type PaymentFormValues = z.infer<typeof paymentSchema>

type PaymentSettingsSectionProps = {
  defaultValues: {
    /** The site currency, set in the currency section. */
    currency: string
    minTopup: string
    amountOptions: string
    amountDiscount: string
  }
}

export function PaymentSettingsSection(props: PaymentSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const [amountOptionsVisual, setAmountOptionsVisual] = useState(true)
  const [amountDiscountVisual, setAmountDiscountVisual] = useState(true)

  const initialValues = useMemo<PaymentFormValues>(
    () => ({
      min_topup: props.defaultValues.minTopup,
      amount_options: formatJsonForEditor(props.defaultValues.amountOptions),
      amount_discount: formatJsonForEditor(props.defaultValues.amountDiscount),
    }),
    [props.defaultValues]
  )
  const form = useForm<PaymentFormValues>({
    resolver: zodResolver(paymentSchema),
    defaultValues: initialValues,
  })
  const isSaving = form.formState.isSubmitting || updateOption.isPending
  useResetForm(form, isSaving ? undefined : initialValues)
  const currency = props.defaultValues.currency

  const onSubmit = async (values: PaymentFormValues) => {
    const updates: Array<{ key: string; value: string }> = []
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

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    try {
      for (const update of updates) {
        await updateOption.mutateAsync(update)
      }
    } catch (error) {
      handleServerError(error)
    }
  }

  return (
    <SettingsSection title={t('Payment Settings')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={isSaving}
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
