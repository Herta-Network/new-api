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
import { Send } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
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
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { parseQuotaFromDollars, quotaUnitsToDollars } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  SettingsControlChildren,
  SettingsControlGroup,
  SettingsForm,
  SettingsFormGrid,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'
import type { EmailProvider } from '../types'
import { EMAIL_PROVIDER_LABELS } from './email-api'
import {
  CloudflareFields,
  ResendFields,
  SmtpFields,
} from './email-provider-fields'
import {
  createEmailSchema,
  formatSender,
  getSmtpSecurityMode,
  parseCloudflareSender,
  parseResendSender,
  type EmailFormValues,
} from './email-settings-schema'
import { EmailTestDialog } from './email-test-dialog'

type EmailSettingsSectionProps = {
  defaultValues: Omit<
    EmailFormValues,
    'ResendSenderName' | 'CloudflareSenderName'
  >
}

const EMAIL_PROVIDERS: EmailProvider[] = ['smtp', 'resend', 'cloudflare']
const NO_BACKUP = 'none'

export function EmailSettingsSection(props: EmailSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const emailSchema = createEmailSchema(t)
  const defaultValues = props.defaultValues
  const [testDialogOpen, setTestDialogOpen] = useState(false)
  const initialValues = useMemo(
    () => ({
      ...defaultValues,
      // A backup equal to the primary sends nothing; it shows as none.
      EmailBackupProvider:
        defaultValues.EmailBackupProvider === defaultValues.EmailProvider
          ? ('' as const)
          : defaultValues.EmailBackupProvider,
      ...parseResendSender(defaultValues.ResendFrom),
      ...parseCloudflareSender(defaultValues.CloudflareEmailFrom),
      QuotaRemindThreshold: String(
        quotaUnitsToDollars(Number(defaultValues.QuotaRemindThreshold) || 0)
      ),
    }),
    [defaultValues]
  )

  const form = useForm<EmailFormValues>({
    resolver: zodResolver(emailSchema),
    defaultValues: initialValues,
  })

  const [provider, backupProvider, quotaRemindEnabled] = useWatch({
    control: form.control,
    name: ['EmailProvider', 'EmailBackupProvider', 'QuotaRemindEnabled'],
  })
  const providersInUse = backupProvider
    ? [provider, backupProvider]
    : [provider]
  const tokensOnly = getCurrencyDisplay().meta.kind === 'tokens'
  const isSaving = form.formState.isSubmitting || updateOption.isPending
  useResetForm(form, isSaving ? undefined : initialValues)

  const onSubmit = async (values: EmailFormValues) => {
    const inUse = new Set([values.EmailProvider, values.EmailBackupProvider])
    const updates: Array<{ key: string; value: string | boolean }> = []
    const pushChanged = (
      key: string,
      value: string | boolean,
      initial: string | boolean
    ) => {
      if (value !== initial) updates.push({ key, value })
    }

    // Only the methods in use are saved; the others keep their drafts.
    if (inUse.has('resend')) {
      pushChanged(
        'ResendFrom',
        formatSender(values.ResendFrom.trim(), values.ResendSenderName.trim()),
        formatSender(initialValues.ResendFrom, initialValues.ResendSenderName)
      )
      const key = values.ResendApiKey.trim()
      if (key) updates.push({ key: 'ResendApiKey', value: key })
    }
    if (inUse.has('cloudflare')) {
      pushChanged(
        'CloudflareEmailAccountId',
        values.CloudflareEmailAccountId.trim(),
        initialValues.CloudflareEmailAccountId.trim()
      )
      pushChanged(
        'CloudflareEmailFrom',
        formatSender(
          values.CloudflareEmailFrom.trim(),
          values.CloudflareSenderName.trim()
        ),
        formatSender(
          initialValues.CloudflareEmailFrom,
          initialValues.CloudflareSenderName
        )
      )
      const token = values.CloudflareEmailApiToken.trim()
      if (token) updates.push({ key: 'CloudflareEmailApiToken', value: token })
    }
    if (inUse.has('smtp')) {
      const securityMode = getSmtpSecurityMode(values)
      for (const key of [
        'SMTPServer',
        'SMTPPort',
        'SMTPAccount',
        'SMTPFrom',
      ] as const) {
        pushChanged(key, values[key].trim(), defaultValues[key].trim())
      }
      const token = values.SMTPToken.trim()
      if (token && token !== defaultValues.SMTPToken.trim()) {
        updates.push({ key: 'SMTPToken', value: token })
      }
      pushChanged(
        'SMTPSSLEnabled',
        securityMode === 'ssl_tls',
        defaultValues.SMTPSSLEnabled
      )
      pushChanged(
        'SMTPStartTLSEnabled',
        securityMode === 'starttls',
        defaultValues.SMTPStartTLSEnabled
      )
      pushChanged(
        'SMTPInsecureSkipVerify',
        values.SMTPInsecureSkipVerify,
        defaultValues.SMTPInsecureSkipVerify
      )
      pushChanged(
        'SMTPForceAuthLogin',
        values.SMTPForceAuthLogin,
        defaultValues.SMTPForceAuthLogin
      )
    }

    pushChanged(
      'QuotaRemindEnabled',
      values.QuotaRemindEnabled,
      defaultValues.QuotaRemindEnabled
    )
    const threshold = values.QuotaRemindThreshold.trim()
    if (threshold !== initialValues.QuotaRemindThreshold) {
      updates.push({
        key: 'QuotaRemindThreshold',
        value: String(parseQuotaFromDollars(Number(threshold || 0))),
      })
    }

    // Save credentials first: a failed settings write must not activate a
    // method. The primary goes before the backup, so two methods can swap.
    pushChanged(
      'EmailProvider',
      values.EmailProvider,
      defaultValues.EmailProvider
    )
    pushChanged(
      'EmailBackupProvider',
      values.EmailBackupProvider,
      defaultValues.EmailBackupProvider
    )

    try {
      for (const update of updates) {
        await updateOption.mutateAsync(update)
        if (update.key === 'ResendApiKey') {
          form.setValue('ResendKeyConfigured', true)
        }
        if (update.key === 'CloudflareEmailApiToken') {
          form.setValue('CloudflareEmailTokenConfigured', true)
        }
      }
      form.setValue('ResendApiKey', '')
      form.setValue('CloudflareEmailApiToken', '')
    } catch (error) {
      handleServerError(error)
    }
  }

  return (
    <SettingsSection title={t('Email & Notifications')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={isSaving}
          />
          <FormField
            control={form.control}
            name='EmailProvider'
            render={({ field }) => (
              <FormItem data-settings-form-span='full'>
                <FormLabel>{t('Email sending method')}</FormLabel>
                <FormControl>
                  <RadioGroup
                    value={field.value}
                    onValueChange={(value) => {
                      field.onChange(value)
                      // The new primary cannot also be the backup.
                      if (form.getValues('EmailBackupProvider') === value) {
                        form.setValue('EmailBackupProvider', '', {
                          shouldDirty: true,
                        })
                      }
                    }}
                    disabled={isSaving}
                    className='grid-cols-1 gap-3 sm:grid-cols-3'
                  >
                    {EMAIL_PROVIDERS.map((item) => (
                      <Label
                        key={item}
                        className='flex cursor-pointer items-center gap-3 rounded-lg border p-4'
                        htmlFor={`email-provider-${item}`}
                      >
                        <RadioGroupItem
                          value={item}
                          id={`email-provider-${item}`}
                        />
                        {EMAIL_PROVIDER_LABELS[item]}
                      </Label>
                    ))}
                  </RadioGroup>
                </FormControl>
                <FormDescription>
                  {t(
                    'The primary method sends every email. Methods not in use keep their settings.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='EmailBackupProvider'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Backup sending method')}</FormLabel>
                <Select
                  items={[
                    { value: NO_BACKUP, label: t('No backup') },
                    ...EMAIL_PROVIDERS.filter((item) => item !== provider).map(
                      (item) => ({
                        value: item,
                        label: EMAIL_PROVIDER_LABELS[item],
                      })
                    ),
                  ]}
                  value={field.value || NO_BACKUP}
                  onValueChange={(value) =>
                    field.onChange(value === NO_BACKUP ? '' : value)
                  }
                  disabled={isSaving}
                >
                  <FormControl>
                    <SelectTrigger
                      aria-label={t('Backup sending method')}
                      className='w-full'
                    >
                      <SelectValue />
                    </SelectTrigger>
                  </FormControl>
                  <SelectContent alignItemWithTrigger={false}>
                    <SelectGroup>
                      <SelectItem value={NO_BACKUP}>
                        {t('No backup')}
                      </SelectItem>
                      {EMAIL_PROVIDERS.filter((item) => item !== provider).map(
                        (item) => (
                          <SelectItem key={item} value={item}>
                            {EMAIL_PROVIDER_LABELS[item]}
                          </SelectItem>
                        )
                      )}
                    </SelectGroup>
                  </SelectContent>
                </Select>
                <FormDescription>
                  {t(
                    'Sends an email only when the primary method certainly did not, such as a refused request or an unreachable server. After a timeout the email is not sent again, so nobody receives it twice.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
          {providersInUse.map((item) => (
            <fieldset
              key={item}
              data-settings-form-span='full'
              className='flex min-w-0 flex-col gap-4'
            >
              <legend className='mb-4 flex items-center gap-2 text-sm font-medium'>
                {EMAIL_PROVIDER_LABELS[item]}
                <Badge variant='outline'>
                  {item === provider ? t('Primary') : t('Backup')}
                </Badge>
              </legend>
              <SettingsFormGrid>
                {item === 'smtp' && (
                  <SmtpFields form={form} isSaving={isSaving} />
                )}
                {item === 'resend' && (
                  <ResendFields form={form} isSaving={isSaving} />
                )}
                {item === 'cloudflare' && (
                  <CloudflareFields form={form} isSaving={isSaving} />
                )}
              </SettingsFormGrid>
            </fieldset>
          ))}
          <div className='flex min-w-0 flex-col gap-2'>
            <span className='text-sm font-medium'>{t('Test email')}</span>
            <div>
              <Button
                type='button'
                variant='outline'
                onClick={() => setTestDialogOpen(true)}
                disabled={isSaving || form.formState.isDirty}
              >
                <Send data-icon='inline-start' />
                {t('Send test email')}
              </Button>
            </div>
            <p className='text-muted-foreground text-sm'>
              {form.formState.isDirty
                ? t('Save your changes before sending a test email')
                : t(
                    'Sends a sample email through one method with the saved settings'
                  )}
            </p>
          </div>
          <SettingsControlGroup>
            <FormField
              control={form.control}
              name='QuotaRemindEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Low balance reminder')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Remind users through their chosen notification method when their balance drops below the threshold'
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

            {quotaRemindEnabled && (
              <SettingsControlChildren>
                <FormField
                  control={form.control}
                  name='QuotaRemindThreshold'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {t('Default reminder threshold')} ({getCurrencyLabel()})
                      </FormLabel>
                      <FormControl>
                        <Input
                          autoComplete='off'
                          type='number'
                          min={0}
                          step={tokensOnly ? 1 : 0.000001}
                          {...field}
                          onChange={(event) =>
                            field.onChange(event.target.value)
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </SettingsControlChildren>
            )}
          </SettingsControlGroup>
        </SettingsForm>
      </Form>
      <EmailTestDialog
        open={testDialogOpen}
        onOpenChange={setTestDialogOpen}
        provider={defaultValues.EmailProvider}
      />
    </SettingsSection>
  )
}
