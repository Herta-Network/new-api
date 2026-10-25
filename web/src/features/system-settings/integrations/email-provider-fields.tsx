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
import { useWatch, type UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { PasswordInput } from '@/components/password-input'
import {
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
import { Switch } from '@/components/ui/switch'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import {
  getSmtpSecurityMode,
  type EmailFormValues,
  type SmtpSecurityMode,
} from './email-settings-schema'

type EmailProviderFieldsProps = {
  form: UseFormReturn<EmailFormValues>
  isSaving: boolean
}

/** The settings of the SMTP sending method. */
export function SmtpFields(props: EmailProviderFieldsProps) {
  const { t } = useTranslation()
  const form = props.form
  const [smtpSSL, smtpStartTLS] = useWatch({
    control: form.control,
    name: ['SMTPSSLEnabled', 'SMTPStartTLSEnabled'],
  })
  return (
    <>
      <FormField
        control={form.control}
        name='SMTPServer'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('SMTP Host')}</FormLabel>
            <FormControl>
              <Input
                autoComplete='off'
                placeholder={t('smtp.example.com')}
                {...field}
                onChange={(event) => field.onChange(event.target.value)}
              />
            </FormControl>
            <FormDescription>
              {t('Hostname or IP of your SMTP provider')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <div data-settings-form-span='full' className='grid gap-6 md:grid-cols-2'>
        <FormField
          control={form.control}
          name='SMTPPort'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Port')}</FormLabel>
              <FormControl>
                <Input
                  autoComplete='off'
                  type='number'
                  placeholder='587'
                  {...field}
                  onChange={(event) => field.onChange(event.target.value)}
                />
              </FormControl>
              <FormDescription>
                {t('Common ports include 25, 465, and 587')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

        <FormItem>
          <FormLabel>{t('SMTP encryption')}</FormLabel>
          <FormControl>
            <RadioGroup
              value={getSmtpSecurityMode({
                SMTPSSLEnabled: smtpSSL,
                SMTPStartTLSEnabled: smtpStartTLS,
              })}
              onValueChange={(value) => {
                const mode = value as SmtpSecurityMode
                form.setValue('SMTPSSLEnabled', mode === 'ssl_tls', {
                  shouldDirty: true,
                })
                form.setValue('SMTPStartTLSEnabled', mode === 'starttls', {
                  shouldDirty: true,
                })
              }}
              className='gap-3'
            >
              <div className='flex items-center gap-2'>
                <RadioGroupItem value='none' id='smtp-security-none' />
                <Label
                  htmlFor='smtp-security-none'
                  className='cursor-pointer font-normal'
                >
                  {t('No encryption')}
                </Label>
              </div>
              <div className='flex items-center gap-2'>
                <RadioGroupItem value='ssl_tls' id='smtp-security-ssl-tls' />
                <Label
                  htmlFor='smtp-security-ssl-tls'
                  className='cursor-pointer font-normal'
                >
                  {t('SSL/TLS')}
                </Label>
              </div>
              <div className='flex items-center gap-2'>
                <RadioGroupItem value='starttls' id='smtp-security-starttls' />
                <Label
                  htmlFor='smtp-security-starttls'
                  className='cursor-pointer font-normal'
                >
                  {t('STARTTLS')}
                </Label>
              </div>
            </RadioGroup>
          </FormControl>
          <FormDescription>
            {t('Choose one SMTP transport security mode')}
          </FormDescription>
        </FormItem>

        <FormField
          control={form.control}
          name='SMTPInsecureSkipVerify'
          render={({ field }) => (
            <SettingsSwitchItem>
              <SettingsSwitchContent>
                <FormLabel>
                  {t('Skip SMTP TLS certificate verification')}
                </FormLabel>
                <FormDescription>
                  {t(
                    'Allow self-signed or hostname-mismatched SMTP certificates'
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

        <FormField
          control={form.control}
          name='SMTPForceAuthLogin'
          render={({ field }) => (
            <SettingsSwitchItem>
              <SettingsSwitchContent>
                <FormLabel>{t('Force AUTH LOGIN')}</FormLabel>
                <FormDescription>
                  {t('Force SMTP authentication using AUTH LOGIN method')}
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
      </div>

      <FormField
        control={form.control}
        name='SMTPAccount'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Username')}</FormLabel>
            <FormControl>
              <Input
                autoComplete='off'
                placeholder={t('noreply@example.com')}
                {...field}
                onChange={(event) => field.onChange(event.target.value)}
              />
            </FormControl>
            <FormDescription>
              {t('Account used when authenticating with the SMTP server')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='SMTPFrom'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('From Address')}</FormLabel>
            <FormControl>
              <Input
                autoComplete='off'
                placeholder={t(
                  'noreply@example.com or Name <noreply@example.com>'
                )}
                {...field}
                onChange={(event) => field.onChange(event.target.value)}
              />
            </FormControl>
            <FormDescription>
              {t(
                'An email address, or Name <email address>. Without a name, recipients see the site name.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />

      <FormField
        control={form.control}
        name='SMTPToken'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Password / Access Token')}</FormLabel>
            <FormControl>
              <Input
                autoComplete='off'
                type='password'
                placeholder={t('Enter new token to update')}
                {...field}
                onChange={(event) => field.onChange(event.target.value)}
              />
            </FormControl>
            <FormDescription>
              {t('Leave blank to keep the existing credential')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </>
  )
}

/** The settings of the Resend sending method. */
export function ResendFields(props: EmailProviderFieldsProps) {
  const { t } = useTranslation()
  const form = props.form
  const isSaving = props.isSaving
  return (
    <>
      <FormField
        control={form.control}
        name='ResendFrom'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Sender email address')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                autoComplete='off'
                disabled={isSaving}
                type='email'
                placeholder='noreply@example.com'
              />
            </FormControl>
            <FormDescription>
              {t('The domain must be verified in Resend')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='ResendSenderName'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Sender name')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                autoComplete='off'
                disabled={isSaving}
                placeholder='New API'
              />
            </FormControl>
            <FormDescription>
              {t('The name recipients see. Leave blank to use the site name.')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='ResendApiKey'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Resend API key')}</FormLabel>
            <FormControl>
              <PasswordInput
                {...field}
                autoComplete='new-password'
                disabled={isSaving}
                placeholder={t('Enter new token to update')}
              />
            </FormControl>
            <FormDescription>
              {t('Leave blank to keep the existing key')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </>
  )
}

/** The settings of the Cloudflare Email Service sending method. */
export function CloudflareFields(props: EmailProviderFieldsProps) {
  const { t } = useTranslation()
  const form = props.form
  const isSaving = props.isSaving
  return (
    <>
      <FormField
        control={form.control}
        name='CloudflareEmailAccountId'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Account ID')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                autoComplete='off'
                disabled={isSaving}
                placeholder='0123456789abcdef0123456789abcdef'
              />
            </FormControl>
            <FormDescription>
              {t('The Cloudflare account that onboarded the sending domain')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='CloudflareEmailApiToken'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Cloudflare API token')}</FormLabel>
            <FormControl>
              <PasswordInput
                {...field}
                autoComplete='new-password'
                disabled={isSaving}
                placeholder={t('Enter new token to update')}
              />
            </FormControl>
            <FormDescription>
              {t(
                'Needs the Email Sending: Edit permission. Leave blank to keep the existing token.'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='CloudflareEmailFrom'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Sender email address')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                autoComplete='off'
                disabled={isSaving}
                type='email'
                placeholder='noreply@example.com'
              />
            </FormControl>
            <FormDescription>
              {t(
                'The domain must use Cloudflare DNS and be onboarded to Email Sending'
              )}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='CloudflareSenderName'
        render={({ field }) => (
          <FormItem>
            <FormLabel>{t('Sender name')}</FormLabel>
            <FormControl>
              <Input
                {...field}
                autoComplete='off'
                disabled={isSaving}
                placeholder='New API'
              />
            </FormControl>
            <FormDescription>
              {t('The name recipients see. Leave blank to use the site name.')}
            </FormDescription>
            <FormMessage />
          </FormItem>
        )}
      />
    </>
  )
}
