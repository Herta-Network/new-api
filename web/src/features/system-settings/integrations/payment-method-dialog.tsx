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
import { ShieldAlert } from 'lucide-react'
import { useEffect } from 'react'
import { useForm, useWatch, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { PasswordInput } from '@/components/password-input'
import { StatusBadge } from '@/components/status-badge'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import {
  applicablePaymentFields,
  createPaymentMethodSchema,
  paymentMethodFormDefaults,
  toPaymentMethodRequest,
  type PaymentMethodFormValues,
} from './payment-method-form'
import {
  BALANCE_PAYMENT_OPTION_ID,
  useSavePaymentMethod,
  type PaymentMethod,
  type PaymentOption,
  type PaymentPlatform,
} from './payment-methods-api'

const PAYMENT_METHOD_FORM_ID = 'payment-method-form'

type PaymentMethodDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The channel to edit; null creates one. */
  method: PaymentMethod | null
  platforms: PaymentPlatform[]
  /** The payment methods a channel can collect. */
  options: PaymentOption[]
  /** Site currency the amount range is entered in. */
  currency: string
}

/**
 * Creates or edits a payment channel: a gateway account, the payment method
 * it collects, and the amounts, priority and weight it takes orders by.
 */
export function PaymentMethodDialog(props: PaymentMethodDialogProps) {
  const { t } = useTranslation()
  const saveMethod = useSavePaymentMethod()
  const secretsSet = props.method?.secrets_set ?? {}
  const fieldsOf = (platform: string) =>
    props.platforms.find((item) => item.platform === platform)?.fields ?? []
  const collectable = props.options.filter(
    (option) => option.id !== BALANCE_PAYMENT_OPTION_ID
  )

  const form = useForm<PaymentMethodFormValues>({
    // The schema follows the platform chosen in the form.
    resolver: (values, context, options) =>
      (
        zodResolver(
          createPaymentMethodSchema(fieldsOf(values.platform), secretsSet)
        ) as Resolver<PaymentMethodFormValues>
      )(values, context, options),
    defaultValues: paymentMethodFormDefaults([], null, ''),
  })
  const [platform, config] = useWatch({
    control: form.control,
    name: ['platform', 'config'],
  })
  // A field shown only under a choice of another one follows that choice.
  const fields = applicablePaymentFields(fieldsOf(platform), config ?? {})
  const isEditing = props.method !== null

  useEffect(() => {
    if (!props.open) return
    const initialPlatform =
      props.method?.platform ?? props.platforms[0]?.platform ?? ''
    form.reset(
      paymentMethodFormDefaults(
        props.platforms.find((item) => item.platform === initialPlatform)
          ?.fields ?? [],
        props.method,
        initialPlatform,
        collectable.length === 1 ? collectable[0].id : undefined
      )
    )
    // collectable follows props.options.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.open, props.method, props.platforms, props.options, form])

  const onSubmit = async (values: PaymentMethodFormValues) => {
    await saveMethod.mutateAsync({
      id: props.method?.id,
      input: toPaymentMethodRequest(values, fieldsOf(values.platform)),
    })
    props.onOpenChange(false)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={isEditing ? t('Edit payment channel') : t('Add payment channel')}
      contentClassName='max-h-[min(85dvh,var(--dialog-available-height))] overflow-y-auto sm:max-w-2xl'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={saveMethod.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form={PAYMENT_METHOD_FORM_ID}
            disabled={saveMethod.isPending}
          >
            {saveMethod.isPending ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <SettingsForm
          id={PAYMENT_METHOD_FORM_ID}
          onSubmit={(event) => {
            // A failed save keeps the dialog open; the shared mutation
            // error handler reports the server's reason.
            void form
              .handleSubmit(onSubmit)(event)
              .catch(() => undefined)
          }}
          autoComplete='off'
        >
          {isEditing ? (
            <FormItem>
              <FormLabel>{t('Platform')}</FormLabel>
              <Input
                value={
                  props.platforms.find((item) => item.platform === platform)
                    ?.name ?? platform
                }
                disabled
                readOnly
              />
              <FormDescription>
                {t('The platform cannot be changed after creation.')}
              </FormDescription>
            </FormItem>
          ) : (
            <FormField
              control={form.control}
              name='platform'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Platform')}</FormLabel>
                  <Select
                    items={props.platforms.map((item) => ({
                      value: item.platform,
                      label: item.name,
                    }))}
                    value={field.value}
                    onValueChange={(value) => {
                      const next = String(value ?? '')
                      field.onChange(next)
                      form.setValue(
                        'config',
                        paymentMethodFormDefaults(fieldsOf(next), null, next)
                          .config
                      )
                    }}
                  >
                    <FormControl>
                      <SelectTrigger className='w-full'>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {props.platforms.map((item) => (
                          <SelectItem key={item.platform} value={item.platform}>
                            {item.name}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}

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
                  {t('Only administrators see the channel name')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='option_id'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Payment Method')}</FormLabel>
                <Select
                  items={collectable.map((option) => ({
                    value: String(option.id),
                    label: option.name,
                  }))}
                  value={field.value}
                  onValueChange={(value) => field.onChange(String(value ?? ''))}
                >
                  <FormControl>
                    <SelectTrigger className='w-full'>
                      <SelectValue placeholder={t('Choose a payment method')} />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {collectable.map((option) => (
                        <SelectItem key={option.id} value={String(option.id)}>
                          {option.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Buyers choose the payment method; its channels collect it without their knowing which.'
                  )}
                </FormDescription>
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
                    {t(
                      'Take orders of its payment method through this channel'
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

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='min_amount'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Lowest amount ({{currency}})', {
                      currency: props.currency,
                    })}
                  </FormLabel>
                  <FormControl>
                    <Input
                      inputMode='decimal'
                      placeholder={t('No limit')}
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='max_amount'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t('Highest amount ({{currency}})', {
                      currency: props.currency,
                    })}
                  </FormLabel>
                  <FormControl>
                    <Input
                      inputMode='decimal'
                      placeholder={t('No limit')}
                      {...field}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
          <p className='text-muted-foreground -mt-2 text-sm'>
            {t(
              'The channel takes orders whose price, before discounts and fees, is within this range; both ends are included.'
            )}
          </p>

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='priority'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Priority')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      max={1000}
                      step={1}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'The lowest priority is tried first; higher ones step in when it cannot take the order.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='weight'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Weight')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      max={1000}
                      step={1}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Channels of one priority share its orders by weight.')}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          {platform === 'epay' && (
            <Alert>
              <ShieldAlert aria-hidden='true' />
              <AlertTitle>{t('Epay safety reminder')}</AlertTitle>
              <AlertDescription>
                {t(
                  'Epay is a payment protocol, not a specific official website. Verify the provider yourself and do not trust random third-party Epay deployments.'
                )}
              </AlertDescription>
            </Alert>
          )}

          {fields.map((platformField) => (
            <FormField
              key={`${platform}-${platformField.key}`}
              control={form.control}
              name={`config.${platformField.key}`}
              render={({ field }) => {
                let input = <Input {...field} value={field.value ?? ''} />
                if (platformField.secret) {
                  input = (
                    <PasswordInput
                      {...field}
                      value={field.value ?? ''}
                      autoComplete='new-password'
                      placeholder={
                        secretsSet[platformField.key]
                          ? t('Enter new key to update')
                          : undefined
                      }
                    />
                  )
                } else if (platformField.type === 'textarea') {
                  input = (
                    <Textarea
                      {...field}
                      value={field.value ?? ''}
                      rows={4}
                      className='font-mono text-xs break-all'
                    />
                  )
                }
                return (
                  <FormItem>
                    <FormLabel className='flex items-center gap-2'>
                      {t(platformField.label)}
                      {platformField.secret &&
                        secretsSet[platformField.key] && (
                          <StatusBadge
                            label={t('Configured')}
                            variant='success'
                            copyable={false}
                          />
                        )}
                    </FormLabel>
                    {platformField.type === 'select' ? (
                      <Select
                        items={(platformField.options ?? []).map((option) => ({
                          value: option.value,
                          label: t(option.label),
                        }))}
                        value={field.value ?? ''}
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
                            {(platformField.options ?? []).map((option) => (
                              <SelectItem
                                key={option.value}
                                value={option.value}
                              >
                                {t(option.label)}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    ) : (
                      <FormControl>{input}</FormControl>
                    )}
                    {platformField.description && (
                      <FormDescription>
                        {t(platformField.description)}
                      </FormDescription>
                    )}
                    {platformField.secret && secretsSet[platformField.key] && (
                      <FormDescription>
                        {t('Leave blank to keep the existing key')}
                      </FormDescription>
                    )}
                    <FormMessage />
                  </FormItem>
                )
              }}
            />
          ))}

          {platform === 'stripe' && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Saving registers the Stripe webhook for the callback URL automatically.'
              )}
            </p>
          )}

          <FormField
            control={form.control}
            name='domain'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Callback domain')}</FormLabel>
                <FormControl>
                  <Input placeholder='https://pay.example.com' {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'Gateways send payment callbacks to this public address. Leave blank to use the server address.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </Dialog>
  )
}
