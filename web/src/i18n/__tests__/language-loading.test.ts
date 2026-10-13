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
import { createInstance } from 'i18next'
import { describe, expect, it, vi } from 'vitest'

import { languageBackend } from '../language-backend'
import en from '../locales/en.json'

async function createLanguageInstance(language: string) {
  const instance = createInstance().use(languageBackend)
  await instance.init({
    lng: language,
    fallbackLng: 'en',
    supportedLngs: ['en', 'zhCN', 'zhTW', 'fr', 'ru', 'ja', 'vi'],
    load: 'currentOnly',
    partialBundledLanguages: true,
    resources: { en },
    nsSeparator: false,
  })
  return instance
}

describe('on-demand language resources', () => {
  it('loads only the selected language and reuses it after switching back', async () => {
    const read = vi.spyOn(languageBackend, 'read')
    const instance = await createLanguageInstance('zhCN')
    expect(instance.t('Loading...')).toBe('加载中...')
    expect(read.mock.calls.map(([language]) => language)).toEqual(['zhCN'])
    expect(instance.hasResourceBundle('fr', 'translation')).toBe(false)
    await instance.changeLanguage('ja')
    expect(instance.hasResourceBundle('ja', 'translation')).toBe(true)
    await instance.changeLanguage('zhCN')
    expect(read.mock.calls.map(([language]) => language)).toEqual([
      'zhCN',
      'ja',
    ])
  })
  it.each(['en', 'zhCN', 'zhTW', 'fr', 'ru', 'ja', 'vi'])(
    'keeps %s usable on a direct startup',
    async (language) => {
      const instance = await createLanguageInstance(language)
      expect(instance.hasResourceBundle(language, 'translation')).toBe(true)
      expect(instance.t('Loading...')).not.toBe('')
      expect(instance.hasResourceBundle('en', 'translation')).toBe(true)
    }
  )
  it('renders English when the requested bundle cannot be downloaded', async () => {
    vi.spyOn(languageBackend, 'read').mockRejectedValueOnce(
      new Error('offline')
    )
    const instance = await createLanguageInstance('fr')
    expect(instance.t('Loading...')).toBe(en.translation['Loading...'])
    expect(instance.hasResourceBundle('fr', 'translation')).toBe(false)
  })
  it('falls back to English for an unsupported language without another download', async () => {
    const read = vi.spyOn(languageBackend, 'read')
    const instance = await createLanguageInstance('unsupported')
    expect(instance.t('Loading...')).toBe(en.translation['Loading...'])
    expect(read).not.toHaveBeenCalled()
  })
})
