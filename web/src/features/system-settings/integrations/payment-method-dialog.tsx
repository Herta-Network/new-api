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

import { Dialog } from '@/components/dialog'
import { ReactIconByName } from '@/components/react-icon-by-name'
import { Button } from '@/components/ui/button'
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
  const required = z.string().trim().min(1, t('Required'))
  const schema = z.object({
    name: required.max(128, invalid),
    icon: z.string().max(512, invalid),
    sort: z
      .number({ error: invalid })
      .int(invalid)
      .min(-100000, invalid)
      .max(100000, invalid),
    enabled: z.boolean(),
    config: z.object({
      endpoint: z
        .url({ error: invalid })
        .refine((value) => /^https?:\/\//.test(value), t('Invalid parameters')),
      merchant_id: required.regex(/^[a-zA-Z0-9_-]{1,128}$/, invalid),
      key: props.editData?.has_key
        ? z.string().max(512, invalid)
        : required.max(512, invalid),
      payment_type: required.regex(/^[a-zA-Z0-9_-]{1,50}$/, invalid),
    }),
  })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: props.editData ?? {
      name: '',
      icon: 'SiAlipay',
      sort: 0,
      enabled: true,
      config: {
        endpoint: '',
        merchant_id: '',
        key: '',
        payment_type: 'alipay',
      },
    },
  })
  const fields = [
    { name: 'name', label: t('Name'), placeholder: t('e.g., Alipay, WeChat') },
    {
      name: 'config.endpoint',
      label: t('Epay endpoint'),
      placeholder: 'https://pay.example.com',
    },
    {
      name: 'config.merchant_id',
      label: t('Epay merchant ID'),
      placeholder: '10001',
    },
    {
      name: 'config.key',
      label: t('Epay secret key'),
      placeholder: props.editData
        ? t('Leave blank unless rotating the secret')
        : '',
    },
    {
      name: 'config.payment_type',
      label: t('Payment type key'),
      placeholder: 'alipay / wxpay',
    },
    { name: 'icon', label: t('Icon'), placeholder: 'SiAlipay' },
  ] as const
  const icon = form.watch('icon')

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
        'Epay payments are charged in CNY. Existing orders keep their original payment configuration.'
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
                provider: 'epay',
              })
            )(event)
          }}
        >
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
                      placeholder={item.placeholder}
                      disabled={props.saving}
                      type={item.name === 'config.key' ? 'password' : 'text'}
                      autoComplete={
                        item.name === 'config.key' ? 'new-password' : 'off'
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
          {icon && <ReactIconByName name={icon} className='size-5' />}
          <FormField
            control={form.control}
            name='sort'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Sort order')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    disabled={props.saving}
                    {...field}
                    onChange={(event) =>
                      field.onChange(event.target.valueAsNumber)
                    }
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
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
                <FormMessage />
              </FormItem>
            )}
          />
        </form>
      </Form>
    </Dialog>
  )
}
