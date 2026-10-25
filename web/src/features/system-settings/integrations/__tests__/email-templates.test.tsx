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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import type { EmailTemplatesInfo } from '../email-api'
import { EmailTemplatesSection } from '../email-templates-section'

const builtin = {
  subject: '{{.SystemName}} email verification',
  body: '<p>Your verification code is: <strong>{{.Code}}</strong></p>',
}

function templatesInfo(customized: boolean): EmailTemplatesInfo {
  const custom = { subject: 'Your code', body: '<p>{{.Code}}</p>' }
  return {
    templates: [
      {
        kind: 'verification',
        variables: ['Code', 'Minutes'],
        required: ['Code'],
        languages: ['en', 'zh-CN'].map((language) => ({
          language,
          template: customized && language === 'en' ? custom : builtin,
          builtin,
          customized: customized && language === 'en',
          updated_at: 0,
        })),
      },
    ],
    languages: ['en', 'zh-CN'],
    test_kinds: ['test', 'verification'],
    default_language: 'en',
  }
}

function Fixture() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <EmailTemplatesSection />
    </SettingsPageProvider>
  )
}

function renderTemplates(customized = false) {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: templatesInfo(customized) },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture />
    </QueryClientProvider>
  )
  return userEvent.setup()
}

afterEach(() => vi.restoreAllMocks())

describe('email templates', () => {
  it('previews a draft in a sandboxed frame and saves it for its language', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: true, data: { subject: 'Site', html: '<p>654321</p>' } },
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true, data: null } })
    const user = renderTemplates()
    const body = await screen.findByRole('textbox', { name: 'HTML body' })
    expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()

    await user.clear(body)
    await user.type(body, '<p>{{{{.Code}}</p>')
    expect(screen.getByText('Unsaved changes')).toBeVisible()
    await waitFor(() =>
      expect(post).toHaveBeenLastCalledWith('/api/email/templates/preview', {
        kind: 'verification',
        language: 'en',
        subject: builtin.subject,
        body: '<p>{{.Code}}</p>',
      })
    )
    const frame = screen.getByTitle('Email preview')
    expect(frame).toHaveAttribute('sandbox', '')
    await waitFor(() =>
      expect(frame).toHaveAttribute('srcdoc', '<p>654321</p>')
    )

    await user.click(screen.getByRole('button', { name: 'Save Changes' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/email/templates/verification/en', {
        subject: builtin.subject,
        body: '<p>{{.Code}}</p>',
      })
    )
  })

  it('shows why the server refuses a draft instead of a preview', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: false,
        message: 'Unknown email template variable Password',
        message_key: 'Unknown email template variable {{name}}',
        message_params: { name: 'Password' },
      },
    })
    const user = renderTemplates()
    const body = await screen.findByRole('textbox', { name: 'HTML body' })
    await user.clear(body)
    await user.type(body, '{{{{.Password}}')
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Unknown email template variable Password'
    )
    expect(screen.queryByTitle('Email preview')).not.toBeInTheDocument()
  })

  it('restores the built-in template of a customized email after confirmation', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: true, data: { subject: 'Your code', html: '' } },
    })
    const del = vi
      .spyOn(api, 'delete')
      .mockResolvedValue({ data: { success: true, data: null } })
    const user = renderTemplates(true)
    expect(await screen.findByText('Customized')).toBeVisible()
    await user.click(
      screen.getByRole('button', { name: 'Restore built-in template' })
    )
    await user.click(await screen.findByRole('button', { name: 'Restore' }))
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith('/api/email/templates/verification/en')
    )
  })

  it('offers no restore for an email that uses its built-in template', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({
      data: { success: true, data: { subject: '', html: '' } },
    })
    renderTemplates()
    expect(await screen.findByText('Built-in')).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Restore built-in template' })
    ).toBeDisabled()
  })
})
