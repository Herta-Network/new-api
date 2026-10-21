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
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { LanguageSwitcher } from '../language-switcher'

function bundle(translation: Record<string, string>) {
  return Promise.resolve({ default: { translation } })
}

function createLoaders() {
  const unused = () => Promise.reject(new Error('not loaded in this test'))
  return {
    en: vi.fn(() => bundle({ 'Change language': 'Change language' })),
    zhCN: vi.fn(unused),
    zhTW: vi.fn(unused),
    fr: vi.fn(() => bundle({ 'Change language': 'Changer de langue' })),
    ru: vi.fn(unused),
    ja: vi.fn(unused),
    vi: vi.fn(unused),
  } satisfies LocaleLoaders
}

async function renderSwitcher(loaders: LocaleLoaders) {
  localStorage.setItem('i18nextLng', 'en')
  const instance = i18next.createInstance()
  await initI18n(instance, loaders)
  render(
    <>
      <LanguageSwitcher />
      <Toaster />
    </>
  )
  return instance
}

beforeEach(() => {
  vi.stubEnv('DEV', false)
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role: ROLE.USER,
  })
})

afterEach(() => {
  toast.dismiss()
  useAuthStore.getState().auth.setUser(null)
  vi.unstubAllEnvs()
  localStorage.clear()
})

test('picking a language switches the interface after its bundle loads and saves the choice', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  await renderSwitcher(createLoaders())
  const user = userEvent.setup()

  await user.click(screen.getByRole('button', { name: 'Change language' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Français' }))

  expect(
    await screen.findByRole('button', { name: 'Changer de langue' })
  ).toBeVisible()
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/user/self', { language: 'fr' })
  )
})

test('picking a language whose bundle fails keeps the interface language, reports it and does not save it', async () => {
  const put = vi.spyOn(api, 'put')
  const loaders = createLoaders()
  loaders.fr.mockRejectedValue(new Error('offline'))
  const instance = await renderSwitcher(loaders)
  const user = userEvent.setup()

  await user.click(screen.getByRole('button', { name: 'Change language' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Français' }))

  expect(await screen.findByText('Failed to load')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Change language' })
  ).toBeInTheDocument()
  expect(instance.language).toBe('en')
  expect(put).not.toHaveBeenCalled()
})
