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
import * as z from 'zod'

const EMAIL_ADDRESS = /^[^\s<>@]+@[^\s<>@]+\.[^\s<>@]+$/
const CLOUDFLARE_ACCOUNT_ID = /^[0-9a-fA-F]{32}$/
const EMAIL_PROVIDERS = ['smtp', 'resend', 'cloudflare'] as const

type IssueContext = z.RefinementCtx

function checkSenderName(
  name: string,
  path: string,
  context: IssueContext,
  t: (key: string) => string
) {
  if (/[\r\n]/.test(name) || name.includes('\0')) {
    context.addIssue({
      code: 'custom',
      path: [path],
      message: t('Sender name must not contain line breaks or null characters'),
    })
  }
}

export const createEmailSchema = (t: (key: string) => string) =>
  z
    .object({
      EmailProvider: z.enum(EMAIL_PROVIDERS),
      EmailBackupProvider: z.enum(['', ...EMAIL_PROVIDERS]),
      ResendApiKey: z.string(),
      ResendKeyConfigured: z.boolean(),
      ResendFrom: z.string(),
      ResendSenderName: z.string(),
      CloudflareEmailAccountId: z.string(),
      CloudflareEmailApiToken: z.string(),
      CloudflareEmailTokenConfigured: z.boolean(),
      CloudflareEmailFrom: z.string(),
      CloudflareSenderName: z.string(),
      SMTPServer: z.string(),
      SMTPPort: z.string(),
      SMTPAccount: z.string(),
      SMTPFrom: z.string(),
      SMTPToken: z.string(),
      SMTPSSLEnabled: z.boolean(),
      SMTPStartTLSEnabled: z.boolean(),
      SMTPInsecureSkipVerify: z.boolean(),
      SMTPForceAuthLogin: z.boolean(),
      QuotaRemindEnabled: z.boolean(),
      // Entered as a display amount; stored in quota units.
      QuotaRemindThreshold: z.string().refine((value) => {
        const trimmed = value.trim()
        if (!trimmed) return true
        return Number.isFinite(Number(trimmed)) && Number(trimmed) >= 0
      }, t('Must be greater than or equal to 0')),
    })
    .superRefine((values, context) => {
      // Only the methods in use are checked; the others keep their drafts.
      const inUse = new Set([values.EmailProvider, values.EmailBackupProvider])
      if (inUse.has('resend')) {
        if (!values.ResendKeyConfigured && !values.ResendApiKey.trim()) {
          context.addIssue({
            code: 'custom',
            path: ['ResendApiKey'],
            message: t('Configure a Resend API key before enabling Resend'),
          })
        }
        if (!EMAIL_ADDRESS.test(values.ResendFrom.trim())) {
          context.addIssue({
            code: 'custom',
            path: ['ResendFrom'],
            message: t('Enter a valid sender email address'),
          })
        }
        checkSenderName(values.ResendSenderName, 'ResendSenderName', context, t)
      }
      if (inUse.has('cloudflare')) {
        if (
          !CLOUDFLARE_ACCOUNT_ID.test(values.CloudflareEmailAccountId.trim())
        ) {
          context.addIssue({
            code: 'custom',
            path: ['CloudflareEmailAccountId'],
            message: t('Enter the 32-character Cloudflare account ID'),
          })
        }
        if (
          !values.CloudflareEmailTokenConfigured &&
          !values.CloudflareEmailApiToken.trim()
        ) {
          context.addIssue({
            code: 'custom',
            path: ['CloudflareEmailApiToken'],
            message: t(
              'Configure a Cloudflare API token before enabling Cloudflare'
            ),
          })
        }
        if (!EMAIL_ADDRESS.test(values.CloudflareEmailFrom.trim())) {
          context.addIssue({
            code: 'custom',
            path: ['CloudflareEmailFrom'],
            message: t('Enter a valid sender email address'),
          })
        }
        checkSenderName(
          values.CloudflareSenderName,
          'CloudflareSenderName',
          context,
          t
        )
      }
      if (!inUse.has('smtp')) return
      const port = values.SMTPPort.trim()
      if (
        port &&
        (!/^\d+$/.test(port) || Number(port) < 1 || Number(port) > 65535)
      ) {
        context.addIssue({
          code: 'custom',
          path: ['SMTPPort'],
          message: t('Port must be a positive integer'),
        })
      }
      // A bare address or "Name <address>"; the server parses the full value.
      const from = values.SMTPFrom.trim()
      const address = from.match(/^[^<>]*<([^<>]+)>$/)?.[1] ?? from
      const hasLineBreak = /[\r\n]/.test(from) || from.includes('\0')
      if (from && (hasLineBreak || !EMAIL_ADDRESS.test(address))) {
        context.addIssue({
          code: 'custom',
          path: ['SMTPFrom'],
          message: t(
            'Enter an email address or Name <email address>, or leave blank'
          ),
        })
      }
    })

export type EmailFormValues = z.infer<ReturnType<typeof createEmailSchema>>

export type SmtpSecurityMode = 'none' | 'ssl_tls' | 'starttls'

export function getSmtpSecurityMode(values: {
  SMTPSSLEnabled: boolean
  SMTPStartTLSEnabled: boolean
}): SmtpSecurityMode {
  if (values.SMTPSSLEnabled) return 'ssl_tls'
  if (values.SMTPStartTLSEnabled) return 'starttls'
  return 'none'
}

/** Splits a sender option, a bare address or "Name <address>". */
export function parseSender(value: string) {
  const named = value.trim().match(/^(.*?)\s*<([^<>]+)>$/)
  if (!named) return { address: value.trim(), name: '' }
  let name = named[1].trim()
  if (name.startsWith('"') && name.endsWith('"')) {
    name = name.slice(1, -1).replaceAll(/\\(.)/g, '$1')
  }
  return { address: named[2].trim(), name }
}

/**
 * Joins a sender address and display name into the option value. The name
 * is quoted so commas, quotes and angle brackets stay text, not mailbox
 * syntax; the server also validates the combined address.
 */
export function formatSender(address: string, name: string) {
  if (!name) return address
  const escapedName = name.replaceAll('\\', '\\\\').replaceAll('"', '\\"')
  return `"${escapedName}" <${address}>`
}

// Keep the existing ResendFrom option/API contract while editing its two parts.
export function parseResendSender(value: string) {
  const sender = parseSender(value)
  return { ResendFrom: sender.address, ResendSenderName: sender.name }
}

export function parseCloudflareSender(value: string) {
  const sender = parseSender(value)
  return {
    CloudflareEmailFrom: sender.address,
    CloudflareSenderName: sender.name,
  }
}
