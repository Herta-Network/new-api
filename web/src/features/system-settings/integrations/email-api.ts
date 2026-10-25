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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { EmailProvider } from '../types'

type ApiResponse<T> = {
  success: boolean
  message?: string
  data: T
}

/** The source of an email: a subject and an HTML body in Go template syntax. */
export type EmailTemplate = {
  /** Empty for the layout, which has no subject. */
  subject: string
  body: string
}

export type EmailTemplateLanguage = {
  /** A backend language: en, zh-CN or zh-TW. */
  language: string
  template: EmailTemplate
  builtin: EmailTemplate
  customized: boolean
  updated_at: number
}

/** An email administrators can edit, with its template in each language. */
export type EmailTemplateKind = {
  kind: string
  /** Variables its sender fills; SystemName is always available. */
  variables: string[]
  /** Variables the body must use. */
  required: string[]
  languages: EmailTemplateLanguage[]
}

export type EmailTemplatesInfo = {
  templates: EmailTemplateKind[]
  languages: string[]
  /** Emails a test can send: 'test' and the editable emails but the layout. */
  test_kinds: string[]
  /** The language of readers who state none (DEFAULT_LANGUAGE). */
  default_language: string
}

export type EmailPreview = {
  subject: string
  html: string
}

/** The layout wraps every email; it has no subject. */
export const EMAIL_LAYOUT_KIND = 'layout'

/** Labels of the emails, English source text. */
export const EMAIL_KIND_LABELS: Record<string, string> = {
  test: 'Test email',
  verification: 'Email verification code',
  password_reset: 'Password reset',
  binding_new: 'Email binding code (new address)',
  binding_current: 'Email change confirmation (current address)',
  security_notice: 'Account security notice',
  quota_low: 'Low balance reminder',
  layout: 'Layout (wraps every email)',
}

/** Names of the backend email languages, English source text. */
export const EMAIL_LANGUAGE_LABELS: Record<string, string> = {
  en: 'English',
  'zh-CN': 'Simplified Chinese',
  'zh-TW': 'Traditional Chinese',
}

export const EMAIL_PROVIDER_LABELS: Record<EmailProvider, string> = {
  smtp: 'SMTP',
  resend: 'Resend API',
  cloudflare: 'Cloudflare Email Service',
}

const emailTemplatesQueryKey = ['email', 'templates'] as const

export function useEmailTemplates(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: emailTemplatesQueryKey,
    enabled: options.enabled ?? true,
    queryFn: async () => {
      const res = await api.get<ApiResponse<EmailTemplatesInfo>>(
        '/api/email/templates'
      )
      return requireServerSuccess(res.data).data
    },
  })
}

type EmailTemplateTarget = {
  kind: string
  language: string
}

function templatePath(target: EmailTemplateTarget) {
  return `/api/email/templates/${encodeURIComponent(target.kind)}/${encodeURIComponent(target.language)}`
}

export function useSaveEmailTemplate() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (
      request: EmailTemplateTarget & { template: EmailTemplate }
    ) => {
      const res = await api.put<ApiResponse<null>>(
        templatePath(request),
        request.template
      )
      return requireServerSuccess(res.data)
    },
    onSuccess: () => {
      toast.success(i18next.t('Email template saved'))
      void queryClient.invalidateQueries({ queryKey: emailTemplatesQueryKey })
    },
  })
}

export function useResetEmailTemplate() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (target: EmailTemplateTarget) => {
      const res = await api.delete<ApiResponse<null>>(templatePath(target))
      return requireServerSuccess(res.data)
    },
    onSuccess: () => {
      toast.success(i18next.t('Built-in template restored'))
      void queryClient.invalidateQueries({ queryKey: emailTemplatesQueryKey })
    },
  })
}

/**
 * Renders a draft with sample values. A draft the server refuses (invalid
 * syntax, an unknown variable) is an error the editor shows by the preview.
 */
export function useEmailTemplatePreview(
  request: (EmailTemplateTarget & { template: EmailTemplate }) | null
) {
  return useQuery({
    queryKey: ['email', 'preview', request],
    enabled: request !== null,
    retry: false,
    meta: { errorToast: false },
    queryFn: async () => {
      if (!request) return null
      const res = await api.post<ApiResponse<EmailPreview>>(
        '/api/email/templates/preview',
        {
          kind: request.kind,
          language: request.language,
          ...request.template,
        }
      )
      return requireServerSuccess(res.data).data
    },
  })
}

export type TestEmailRequest = {
  provider: EmailProvider
  kind: string
  language: string
  to: string
}

/** Sends a sample email through one method, with the saved settings. */
export function useSendTestEmail() {
  return useMutation({
    mutationFn: async (request: TestEmailRequest) => {
      const res = await api.post<ApiResponse<null>>('/api/email/test', request)
      return requireServerSuccess(res.data)
    },
    onSuccess: (_data, request) => {
      toast.success(
        i18next.t('Test email sent to {{address}}', { address: request.to })
      )
    },
  })
}
