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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { Toaster, toast } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { initI18n, type LocaleLoaders } from '@/i18n/locale-loader'
import { api } from '@/lib/api'

import { LanguagePreferencesCard } from '../components/language-preferences-card'
import type { UserProfile } from '../types'

const profile: UserProfile = {
  id: 1,
  username: 'alice',
  display_name: 'Alice',
  role: 1,
  group: 'default',
  quota: 1000000,
  used_quota: 0,
  request_count: 0,
  status: 1,
  aff_count: 0,
  aff_quota: 0,
  aff_history_quota: 0,
  created_time: 0,
  setting: JSON.stringify({ language: 'en' }),
}

function bundle(translation: Record<string, string>) {
  return Promise.resolve({ default: { translation } })
}

function createLoaders() {
  const unused = () => Promise.reject(new Error('not loaded in this test'))
  return {
    en: vi.fn(() => bundle({ 'Interface Language': 'Interface Language' })),
    zhCN: vi.fn(unused),
    zhTW: vi.fn(unused),
    fr: vi.fn(() => bundle({ 'Interface Language': "Langue de l'interface" })),
    ru: vi.fn(unused),
    ja: vi.fn(unused),
    vi: vi.fn(unused),
  } satisfies LocaleLoaders
}

async function renderCard(loaders: LocaleLoaders) {
  localStorage.setItem('i18nextLng', 'en')
  const instance = i18next.createInstance()
  await initI18n(instance, loaders)
  const onProfileUpdate = vi.fn()
  render(
    <>
      <LanguagePreferencesCard
        profile={profile}
        onProfileUpdate={onProfileUpdate}
      />
      <Toaster />
    </>
  )
  return { instance, onProfileUpdate }
}

beforeEach(() => {
  vi.stubEnv('DEV', false)
})

afterEach(() => {
  toast.dismiss()
  vi.unstubAllEnvs()
  localStorage.clear()
})

test('choosing a language switches the interface once loaded and saves the preference', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const { onProfileUpdate } = await renderCard(createLoaders())
  const user = userEvent.setup()

  await user.click(screen.getByRole('combobox'))
  await user.click(await screen.findByRole('option', { name: 'Français' }))

  expect(await screen.findByText("Langue de l'interface")).toBeVisible()
  expect(put).toHaveBeenCalledWith('/api/user/self', { language: 'fr' })
  await waitFor(() => expect(onProfileUpdate).toHaveBeenCalled())
})

test('a language whose bundle fails keeps the previous selection, reports it and is not saved', async () => {
  const put = vi.spyOn(api, 'put')
  const loaders = createLoaders()
  loaders.fr.mockRejectedValue(new Error('offline'))
  const { instance, onProfileUpdate } = await renderCard(loaders)
  const user = userEvent.setup()

  await user.click(screen.getByRole('combobox'))
  await user.click(await screen.findByRole('option', { name: 'Français' }))

  expect(await screen.findByText('Failed to load')).toBeVisible()
  expect(screen.getByRole('combobox')).toHaveValue('English')
  expect(screen.getByRole('combobox')).toBeEnabled()
  expect(instance.language).toBe('en')
  expect(put).not.toHaveBeenCalled()
  expect(onProfileUpdate).not.toHaveBeenCalled()
})
