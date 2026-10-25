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
import { useEffect, useMemo } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
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
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { PAYMENT_ICONS } from '@/features/wallet/lib/payment-icons'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import {
  BALANCE_PAYMENT_OPTION_ID,
  PAYMENT_FEE_MODE_OPTIONS,
  useSavePaymentOption,
  type PaymentOption,
} from './payment-methods-api'
import {
  paymentOptionFormDefaults,
  paymentOptionSchema,
  toPaymentOptionRequest,
  usesFeeRate,
  usesFixedFee,
  type PaymentOptionFormValues,
} from './payment-option-form'

const PAYMENT_OPTION_FORM_ID = 'payment-option-form'

type PaymentOptionDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The payment method to edit; null creates one. */
  option: PaymentOption | null
  /** Site currency the fixed fee is entered in. */
  currency: string
}

/**
 * Creates or edits a payment method as buyers see it: its name, icon and
 * the fee they pay whatever channel collects it.
 */
export function PaymentOptionDialog(props: PaymentOptionDialogProps) {
  const { t } = useTranslation()
  const saveOption = useSavePaymentOption()
  const form = useForm<PaymentOptionFormValues>({
    resolver: zodResolver(paymentOptionSchema),
    defaultValues: paymentOptionFormDefaults(null),
  })
  const feeMode = useWatch({ control: form.control, name: 'fee_mode' })
  const isBalance = props.option?.id === BALANCE_PAYMENT_OPTION_ID
  const iconOptions = useMemo(
    () => [
      { value: '', label: t('Default icon') },
      ...PAYMENT_ICONS.map((icon) => ({
        value: icon.name,
        label: t(icon.labelKey),
        icon: <icon.Icon className='size-4' />,
      })),
    ],
    [t]
  )

  useEffect(() => {
    if (props.open) form.reset(paymentOptionFormDefaults(props.option))
  }, [props.open, props.option, form])

  const onSubmit = async (values: PaymentOptionFormValues) => {
    await saveOption.mutateAsync({
      id: props.option?.id,
      input: toPaymentOptionRequest(values, props.option?.id),
    })
    props.onOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.option ? t('Edit payment method') : t('Add payment method')}
      contentClassName='max-h-[min(85dvh,var(--dialog-available-height))] overflow-y-auto sm:max-w-xl'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={saveOption.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form={PAYMENT_OPTION_FORM_ID}
            disabled={saveOption.isPending}
          >
            {saveOption.isPending ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <SettingsForm
          id={PAYMENT_OPTION_FORM_ID}
          onSubmit={(event) => {
            // A failed save keeps the dialog open; the shared mutation
            // error handler reports the server's reason.
            void form
              .handleSubmit(onSubmit)(event)
              .catch(() => undefined)
          }}
          autoComplete='off'
        >
          <FormField
            control={form.control}
            name='name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Name')}</FormLabel>
                <FormControl>
                  <Input {...field} />
                </FormControl>
                <FormDescription>
                  {t('Shown to buyers when they choose how to pay')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='icon'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Icon')}</FormLabel>
                <FormControl>
                  <Combobox
                    ref={field.ref}
                    options={iconOptions}
                    value={field.value}
                    onValueChange={(value) => field.onChange(value ?? '')}
                    onBlur={field.onBlur}
                    showSelectedIcon
                    searchPlaceholder={t('Search icons')}
                    emptyText={t('No matching icons')}
                    className='w-full'
                  />
                </FormControl>
                <FormDescription>
                  {t('Shown to buyers next to the payment method name.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='sort'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Sort')}</FormLabel>
                <FormControl>
                  <Input type='number' step={1} {...field} />
                </FormControl>
                <FormDescription>
                  {t('Methods with a higher value are listed first')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='description'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Description')}</FormLabel>
                <FormControl>
                  <Textarea rows={2} {...field} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enabled')}</FormLabel>
                  <FormDescription>
                    {t('Offer this payment method to buyers')}
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

          {!isBalance && (
            <FormField
              control={form.control}
              name='fee_mode'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Payment fee')}</FormLabel>
                  <Select
                    items={PAYMENT_FEE_MODE_OPTIONS.map((option) => ({
                      value: String(option.value),
                      label: t(option.labelKey),
                    }))}
                    value={field.value}
                    onValueChange={(value) =>
                      field.onChange(String(value ?? ''))
                    }
                  >
                    <FormControl>
                      <SelectTrigger className='w-full'>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {PAYMENT_FEE_MODE_OPTIONS.map((option) => (
                          <SelectItem
                            key={option.value}
                            value={String(option.value)}
                          >
                            {t(option.labelKey)}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'Charged to the buyer on top of the price whatever channel collects it; it does not change the credited quota.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          {!isBalance && usesFeeRate(Number(feeMode)) && (
            <FormField
              control={form.control}
              name='fee_rate'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Fee rate (%)')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      inputMode='decimal'
                      min={0}
                      max={100}
                      step={0.01}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('For example, 2.9 charges 2.9% of the price')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

          {!isBalance && usesFixedFee(Number(feeMode)) && (
            <FormField
              control={form.control}
              name='fee_fixed'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Fixed fee ({{currency}})', {
                      currency: props.currency,
                    })}
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
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
        </SettingsForm>
      </Form>
    </Dialog>
  )
}
