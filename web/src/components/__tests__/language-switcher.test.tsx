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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance, type ResourceKey } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { LanguageSwitcher } from '../language-switcher'

it('persists only the latest choice when language downloads finish out of order', async () => {
  const pending = new Map<string, (data: ResourceKey) => void>()
  const instance = createInstance()
    .use(initReactI18next)
    .use({
      type: 'backend' as const,
      init() {},
      read(language: string, _namespace: string) {
        return new Promise<ResourceKey>((resolve) =>
          pending.set(language, resolve)
        )
      },
    })
  await instance.init({
    lng: 'en',
    fallbackLng: 'en',
    partialBundledLanguages: true,
    resources: { en: { translation: {} } },
  })
  const user = userEvent.setup()
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const previous = useAuthStore.getState().auth.user
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'language-test', role: 1 })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  try {
    render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={instance}>
          <LanguageSwitcher />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await user.click(screen.getByRole('button', { name: 'Change language' }))
    await user.click(screen.getByRole('menuitem', { name: 'Français' }))
    expect(pending.has('fr')).toBe(true)
    await user.click(screen.getByRole('button', { name: 'Change language' }))
    await user.click(screen.getByRole('menuitem', { name: 'Русский' }))
    expect(pending.has('ru')).toBe(true)
    await act(async () => {
      pending.get('ru')?.({})
    })
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith('/api/user/self', { language: 'ru' })
    )
    await act(async () => {
      pending.get('fr')?.({})
    })
    expect(instance.language).toBe('ru')
    expect(put).toHaveBeenCalledTimes(1)
  } finally {
    useAuthStore.getState().auth.setUser(previous)
    client.clear()
  }
})
