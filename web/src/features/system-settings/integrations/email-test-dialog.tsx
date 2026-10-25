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
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import { Dialog } from '@/components/dialog'
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

import type { EmailProvider } from '../types'
import {
  EMAIL_KIND_LABELS,
  EMAIL_LANGUAGE_LABELS,
  EMAIL_PROVIDER_LABELS,
  useEmailTemplates,
  useSendTestEmail,
} from './email-api'

const createTestEmailSchema = (t: (key: string) => string) =>
  z.object({
    to: z.string().trim().email(t('Please enter a valid email address')),
    provider: z.enum(['smtp', 'resend', 'cloudflare']),
    kind: z.string(),
    language: z.string(),
  })

type TestEmailValues = z.infer<ReturnType<typeof createTestEmailSchema>>

type EmailTestDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The saved primary method, chosen when the dialog opens. */
  provider: EmailProvider
}

/**
 * Sends a sample email through one method alone, with the saved settings
 * and templates. A failure shows the provider's reason.
 */
export function EmailTestDialog(props: EmailTestDialogProps) {
  const { t } = useTranslation()
  const templates = useEmailTemplates({ enabled: props.open })
  const sendTestEmail = useSendTestEmail()
  const kinds = templates.data?.test_kinds ?? ['test']
  const languages =
    templates.data?.languages ?? Object.keys(EMAIL_LANGUAGE_LABELS)
  const defaultLanguage = templates.data?.default_language ?? 'en'
  const form = useForm<TestEmailValues>({
    resolver: zodResolver(createTestEmailSchema(t)),
    defaultValues: {
      to: '',
      provider: props.provider,
      kind: 'test',
      language: defaultLanguage,
    },
  })

  // Each opening starts from the saved primary method.
  useEffect(() => {
    if (props.open) {
      form.reset({
        ...form.getValues(),
        provider: props.provider,
        language: defaultLanguage,
      })
    }
  }, [props.open, props.provider, defaultLanguage, form])

  const onSubmit = (values: TestEmailValues) => {
    sendTestEmail.mutate(values, {
      onSuccess: () => props.onOpenChange(false),
    })
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Send test email')}
      description={t(
        'Sends a sample email through the chosen method alone, without a backup, using the saved settings and templates.'
      )}
      contentClassName='sm:max-w-lg'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
            disabled={sendTestEmail.isPending}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form='email-test-form'
            disabled={sendTestEmail.isPending}
          >
            {sendTestEmail.isPending ? t('Sending...') : t('Send')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id='email-test-form'
          className='flex flex-col gap-4'
          onSubmit={form.handleSubmit(onSubmit)}
        >
          <FormField
            control={form.control}
            name='to'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Recipient')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='email'
                    autoComplete='off'
                    placeholder='admin@example.com'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='provider'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Sending method')}</FormLabel>
                <Select
                  items={Object.entries(EMAIL_PROVIDER_LABELS).map(
                    ([value, label]) => ({ value, label })
                  )}
                  value={field.value}
                  onValueChange={field.onChange}
                >
                  <FormControl>
                    <SelectTrigger
                      aria-label={t('Sending method')}
                      className='w-full'
                    >
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {Object.entries(EMAIL_PROVIDER_LABELS).map(
                        ([value, label]) => (
                          <SelectItem key={value} value={value}>
                            {label}
                          </SelectItem>
                        )
                      )}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t('Any method with saved settings can be tested')}
                </FormDescription>
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='kind'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Email')}</FormLabel>
                <Select
                  items={kinds.map((kind) => ({
                    value: kind,
                    label: t(EMAIL_KIND_LABELS[kind] ?? kind),
                  }))}
                  value={field.value}
                  onValueChange={field.onChange}
                >
                  <FormControl>
                    <SelectTrigger aria-label={t('Email')} className='w-full'>
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {kinds.map((kind) => (
                        <SelectItem key={kind} value={kind}>
                          {t(EMAIL_KIND_LABELS[kind] ?? kind)}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t('Templates are filled with sample values')}
                </FormDescription>
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='language'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Email language')}</FormLabel>
                <Select
                  items={languages.map((language) => ({
                    value: language,
                    label: t(EMAIL_LANGUAGE_LABELS[language] ?? language),
                  }))}
                  value={field.value}
                  onValueChange={field.onChange}
                >
                  <FormControl>
                    <SelectTrigger
                      aria-label={t('Email language')}
                      className='w-full'
                    >
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      {languages.map((language) => (
                        <SelectItem key={language} value={language}>
                          {t(EMAIL_LANGUAGE_LABELS[language] ?? language)}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </FormItem>
            )}
          />
        </form>
      </Form>
    </Dialog>
  )
}
