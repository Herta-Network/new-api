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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ReactIconByName } from '@/components/react-icon-by-name'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import type { PaymentMethodData } from './payment-method-api'

const formID = 'payment-method-form'
type Props = {
  editData: PaymentMethodData | null
  onOpenChange: (open: boolean) => void
  onSave: (value: PaymentMethodData) => void
  saving: boolean
}
export function PaymentMethodDialog(props: Props) {
  const { t } = useTranslation()
  const invalid = t('Invalid parameters')
  const schema = z
    .object({
      name: z.string().trim().min(1, t('Required')).max(128, invalid),
      provider: z.enum(['epay', 'stripe', 'cryptomus']),
      icon: z.string().max(512, invalid),
      sort: z
        .number({ error: invalid })
        .int(invalid)
        .min(-100000, invalid)
        .max(100000, invalid),
      enabled: z.boolean(),
      config: z.object({
        key: z.string().max(512, invalid),
        endpoint: z.string(),
        merchant_id: z.string(),
        payment_type: z.string(),
        webhook_secret: z.string(),
        price_id: z.string(),
        promotion_codes: z.boolean(),
        unit_price: z.string(),
        min_topup: z
          .number({ error: invalid })
          .int(invalid)
          .min(0, invalid)
          .max(10000, invalid),
      }),
    })
    .superRefine((values, ctx) => {
      const required = (
        field: keyof typeof values.config,
        condition: boolean
      ) => {
        if (!condition) {
          ctx.addIssue({
            code: 'custom',
            message: invalid,
            path: ['config', field],
          })
        }
      }
      required('key', !!values.config.key.trim() || !!props.editData?.has_key)
      if (values.provider === 'epay') {
        let validURL = false
        try {
          const url = new URL(values.config.endpoint)
          validURL =
            ['http:', 'https:'].includes(url.protocol) &&
            !url.username &&
            !url.password &&
            !url.search &&
            !url.hash
        } catch {
          /* handled as validation */
        }
        required('endpoint', validURL)
        required(
          'merchant_id',
          /^[a-zA-Z0-9_-]{1,128}$/.test(values.config.merchant_id)
        )
        required(
          'payment_type',
          /^[a-zA-Z0-9_-]{1,50}$/.test(values.config.payment_type)
        )
      }
      if (values.provider === 'stripe' && values.enabled) {
        required(
          'webhook_secret',
          values.config.webhook_secret.startsWith('whsec_') ||
            !!props.editData?.has_webhook_secret
        )
      }
      if (values.provider === 'cryptomus') {
        required(
          'merchant_id',
          /^[a-zA-Z0-9_-]{1,128}$/.test(values.config.merchant_id)
        )
        required(
          'unit_price',
          Number(values.config.unit_price) > 0 &&
            Number(values.config.unit_price) <= 10000
        )
      }
    })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: {
      name: '',
      provider: 'epay',
      icon: 'SiAlipay',
      sort: 0,
      enabled: true,
      ...props.editData,
      config: {
        key: '',
        endpoint: '',
        merchant_id: '',
        payment_type: 'alipay',
        webhook_secret: '',
        price_id: '',
        promotion_codes: false,
        unit_price: '1',
        min_topup: 0,
        ...props.editData?.config,
      },
    },
  })
  const provider = form.watch('provider')
  const icon = form.watch('icon')
  const fields: {
    name:
      | 'name'
      | 'icon'
      | 'config.endpoint'
      | 'config.merchant_id'
      | 'config.key'
      | 'config.payment_type'
      | 'config.webhook_secret'
      | 'config.price_id'
      | 'config.unit_price'
    label: string
    secret?: boolean
  }[] = [{ name: 'name', label: t('Name') }]
  if (provider === 'epay') {
    fields.push(
      { name: 'config.endpoint', label: t('Epay endpoint') },
      { name: 'config.merchant_id', label: t('Epay merchant ID') },
      { name: 'config.payment_type', label: t('Payment type key') }
    )
  }
  if (provider === 'cryptomus') {
    fields.push(
      { name: 'config.merchant_id', label: t('Merchant ID') },
      { name: 'config.unit_price', label: t('Unit price (USD)') }
    )
  }
  let keyLabel = t('API Key')
  if (provider === 'epay') keyLabel = t('Epay secret key')
  if (provider === 'stripe') keyLabel = t('Stripe API secret')
  fields.push({ name: 'config.key', label: keyLabel, secret: true })
  if (provider === 'stripe') {
    fields.push(
      {
        name: 'config.webhook_secret',
        label: t('Stripe webhook secret'),
        secret: true,
      },
      { name: 'config.price_id', label: t('Stripe Price ID (top-up)') }
    )
  }
  fields.push({ name: 'icon', label: t('Icon') })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!props.saving) props.onOpenChange(open)
      }}
      title={
        props.editData ? t('Edit payment method') : t('Add payment method')
      }
      description={t(
        'Configure a payment gateway for top-ups and subscriptions.'
      )}
      contentClassName='sm:max-w-xl'
      contentHeight='auto'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={props.saving}
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={formID} disabled={props.saving}>
            {t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={formID}
          className='space-y-4'
          onSubmit={(event) => {
            event.stopPropagation()
            void form.handleSubmit((values) =>
              props.onSave({
                ...values,
                id: props.editData?.id ?? 0,
                version_id: props.editData?.version_id ?? 0,
              })
            )(event)
          }}
        >
          <FormField
            control={form.control}
            name='provider'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Provider')}</FormLabel>
                <FormControl>
                  <Combobox
                    options={[
                      { label: 'Epay', value: 'epay' },
                      { label: 'Stripe', value: 'stripe' },
                      { label: 'Cryptomus', value: 'cryptomus' },
                    ]}
                    value={field.value}
                    onValueChange={(value) => {
                      if (!value) return
                      field.onChange(value)
                      if (!props.editData) {
                        form.setValue('config.key', '')
                        form.setValue('config.webhook_secret', '')
                        form.setValue('enabled', value !== 'stripe')
                        const icons: Record<string, string> = {
                          epay: 'SiAlipay',
                          stripe: 'SiStripe',
                          cryptomus: 'SiBitcoin',
                        }
                        const currentIcon = form.getValues('icon')
                        if (
                          !currentIcon ||
                          Object.values(icons).includes(currentIcon)
                        ) {
                          form.setValue('icon', icons[value] ?? '')
                        }
                      }
                    }}
                    disabled={!!props.editData || props.saving}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          {fields.map((item) => (
            <FormField
              key={item.name}
              control={form.control}
              name={item.name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{item.label}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      disabled={props.saving}
                      type={item.secret ? 'password' : 'text'}
                      autoComplete={item.secret ? 'new-password' : 'off'}
                      placeholder={
                        item.secret && props.editData
                          ? t('Leave blank unless rotating the secret')
                          : ''
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
          {provider === 'stripe' && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Save Stripe as disabled, register this webhook URL, then enter the signing secret and enable payments.'
              )}
            </p>
          )}
          {icon && <ReactIconByName name={icon} className='size-5' />}
          {(['sort', 'config.min_topup'] as const).map((name) => (
            <FormField
              key={name}
              control={form.control}
              name={name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {name === 'sort'
                      ? t('Sort order')
                      : t('Minimum top-up (USD)')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='number'
                      disabled={props.saving}
                      onChange={(event) =>
                        field.onChange(event.target.valueAsNumber)
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
          {provider === 'stripe' && (
            <FormField
              control={form.control}
              name='config.promotion_codes'
              render={({ field }) => (
                <FormItem className='flex items-center justify-between'>
                  <FormLabel>{t('Allow promotion codes')}</FormLabel>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      disabled={props.saving}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
          )}
          <FormField
            control={form.control}
            name='enabled'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between'>
                <FormLabel>{t('Enabled')}</FormLabel>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={props.saving}
                  />
                </FormControl>
              </FormItem>
            )}
          />
          {props.editData?.webhook_url && (
            <div className='space-y-2'>
              <p className='text-sm'>{t('Webhook URL')}</p>
              <div className='flex items-center gap-2'>
                <code className='min-w-0 text-xs break-all'>
                  {props.editData.webhook_url}
                </code>
                <CopyButton value={props.editData.webhook_url} />
              </div>
              {provider === 'stripe' && (
                <p className='text-muted-foreground text-xs break-all'>
                  checkout.session.completed,
                  checkout.session.async_payment_succeeded,
                  checkout.session.async_payment_failed,
                  checkout.session.expired, invoice.paid, invoice.payment_failed
                </p>
              )}
            </div>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
