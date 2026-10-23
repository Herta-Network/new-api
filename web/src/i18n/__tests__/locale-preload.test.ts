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
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { INTERFACE_LANGUAGE_OPTIONS } from '../languages'
import { initI18n, type LocaleLoaders } from '../locale-loader'
import { localePreloadScript } from '../locale-preload'

const chunkUrls = Object.fromEntries(
  INTERFACE_LANGUAGE_OPTIONS.map(({ code }) => [
    code,
    `/static/js/async/locale-${code}.0123456789.js`,
  ])
)

type Browser = { saved?: string; languages: string[] }

function setBrowser(browser: Browser) {
  if (browser.saved) localStorage.setItem('i18nextLng', browser.saved)
  vi.spyOn(navigator, 'languages', 'get').mockReturnValue(browser.languages)
  vi.spyOn(navigator, 'language', 'get').mockReturnValue(
    browser.languages[0] ?? ''
  )
}

function runPreloadScript(): HTMLLinkElement[] {
  // The same source the build inlines in index.html, run in this page.
  new Function(localePreloadScript(chunkUrls))()
  return [...document.head.querySelectorAll<HTMLLinkElement>('link')]
}

/** The languages whose bundle the app downloads at startup. */
async function startupDownloads(): Promise<string[]> {
  const downloaded: string[] = []
  const loaders = Object.fromEntries(
    INTERFACE_LANGUAGE_OPTIONS.map(({ code }) => [
      code,
      (): ReturnType<LocaleLoaders['en']> => {
        downloaded.push(code)
        return Promise.resolve({ default: { translation: { Language: code } } })
      },
    ])
  ) as LocaleLoaders
  await initI18n(i18next.createInstance(), loaders)
  return downloaded
}

beforeEach(() => {
  // i18next debug logging would print every loaded bundle.
  vi.stubEnv('DEV', false)
  localStorage.clear()
})

afterEach(() => {
  vi.unstubAllEnvs()
  vi.restoreAllMocks()
  localStorage.clear()
  document.head.replaceChildren()
})

describe('locale preload script', () => {
  test.each(INTERFACE_LANGUAGE_OPTIONS.map(({ code }) => code))(
    'saved language %s is preloaded and is the one the app downloads',
    async (code) => {
      setBrowser({ saved: code, languages: ['de-DE'] })

      const links = runPreloadScript()

      expect(links.map((link) => link.getAttribute('href'))).toEqual([
        chunkUrls[code],
      ])
      await expect(startupDownloads()).resolves.toEqual([code])
    }
  )

  test.each([
    { languages: ['zh-CN'], expected: 'zhCN' },
    { languages: ['zh'], expected: 'zhCN' },
    { languages: ['zh-SG'], expected: 'zhCN' },
    { languages: ['zh-TW'], expected: 'zhTW' },
    { languages: ['zh-HK'], expected: 'zhTW' },
    { languages: ['zh-Hant-TW'], expected: 'zhTW' },
    { languages: ['en-US'], expected: 'en' },
    { languages: ['fr-FR'], expected: 'fr' },
    { languages: ['ru'], expected: 'ru' },
    { languages: ['ja_JP'], expected: 'ja' },
    { languages: ['vi-VN'], expected: 'vi' },
    { languages: ['pt-BR'], expected: 'en' },
    { languages: [], expected: 'en' },
    { languages: ['de-DE', 'fr'], expected: 'fr' },
    // A supported code anywhere in the list beats a region variant before it.
    { languages: ['en-US', 'zh-CN'], expected: 'zhCN' },
  ])(
    'browser languages $languages without a saved choice preload $expected',
    async ({ languages, expected }) => {
      setBrowser({ languages })

      const links = runPreloadScript()

      expect(links.map((link) => link.getAttribute('href'))).toEqual([
        chunkUrls[expected],
      ])
      await expect(startupDownloads()).resolves.toEqual([expected])
    }
  )

  test.each([
    { saved: 'zh-TW', languages: ['en-US'], expected: 'zhTW' },
    { saved: 'de', languages: ['ja-JP'], expected: 'ja' },
  ])(
    'saved value $saved with browser languages $languages preloads $expected',
    async ({ saved, languages, expected }) => {
      setBrowser({ saved, languages })

      const links = runPreloadScript()

      expect(links.map((link) => link.getAttribute('href'))).toEqual([
        chunkUrls[expected],
      ])
      await expect(startupDownloads()).resolves.toEqual([expected])
    }
  )

  // The language detector skips storage that fails its first check and caches
  // that result for the page, so only the script side runs with it blocked.
  test('blocked storage still preloads the browser language', () => {
    setBrowser({ languages: ['ja-JP'] })
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('Storage is blocked', 'SecurityError')
    })

    const links = runPreloadScript()

    expect(links.map((link) => link.getAttribute('href'))).toEqual([
      chunkUrls.ja,
    ])
  })

  test('preload link is a credentialed script request the chunk loader reuses', () => {
    setBrowser({ saved: 'zhCN', languages: ['zh-CN'] })

    const [link] = runPreloadScript()

    // Rspack loads async chunks with a plain `<script src>` (no
    // `crossorigin`), so only an `as="script"` preload without `crossorigin`
    // is matched to that request instead of downloading the chunk twice.
    expect(link.rel).toBe('preload')
    expect(link.as).toBe('script')
    expect(link.hasAttribute('crossorigin')).toBe(false)
    // As low as the bundle's deferred scripts, so it never delays them.
    expect(link.getAttribute('fetchpriority')).toBe('low')
  })
})
