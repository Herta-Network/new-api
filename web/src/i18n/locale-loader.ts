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
import type { BackendModule, i18n as I18n } from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'

import {
  convertDetectedLanguage,
  INTERFACE_LANGUAGE_OPTIONS,
  type InterfaceLanguageCode,
} from './languages'

type LocaleFile = { translation: Record<string, string> }

export type LocaleLoaders = Record<
  InterfaceLanguageCode,
  () => Promise<{ default: LocaleFile }>
>

type LanguageChangeResult = 'changed' | 'failed' | 'superseded'

// One dynamic import per language: Rsbuild emits each locale file as its own
// chunk, so a page downloads only the languages it actually shows.
const localeLoaders: LocaleLoaders = {
  en: () => import(/* webpackChunkName: "locale-en" */ './locales/en.json'),
  zhCN: () => import(/* webpackChunkName: "locale-zh" */ './locales/zh.json'),
  zhTW: () =>
    import(/* webpackChunkName: "locale-zh-TW" */ './locales/zh-TW.json'),
  fr: () => import(/* webpackChunkName: "locale-fr" */ './locales/fr.json'),
  ru: () => import(/* webpackChunkName: "locale-ru" */ './locales/ru.json'),
  ja: () => import(/* webpackChunkName: "locale-ja" */ './locales/ja.json'),
  vi: () => import(/* webpackChunkName: "locale-vi" */ './locales/vi.json'),
}

/**
 * Initialize `instance` with the detected interface language.
 *
 * The promise resolves once that language's bundle is in the store. If it
 * fails to load, English is loaded as the fallback for this page view and the
 * saved preference is kept, so the next page load tries the language again.
 */
export async function initI18n(
  instance: I18n,
  loaders: LocaleLoaders = localeLoaders
): Promise<void> {
  const failedLanguages = new Set<string>()
  const backend: BackendModule = {
    type: 'backend',
    init: () => undefined,
    async read(language, _namespace, callback) {
      let file: { default: LocaleFile }
      try {
        file = await loaders[language as InterfaceLanguageCode]()
      } catch (error) {
        failedLanguages.add(language)
        // `true` marks the read as retryable, so a later explicit load (a
        // language switch) can request this language again.
        callback(error as Error, true)
        return
      }
      failedLanguages.delete(language)
      callback(null, file.default.translation)
    },
  }

  await instance
    .use(backend)
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
      // Every locale file carries the full key set (`bun run i18n:sync` keeps
      // them aligned), so English is only needed for a language whose bundle
      // failed to load. Without a code, i18next is picking the language for an
      // unsupported detection, which stays English.
      fallbackLng: (code) => (code && !failedLanguages.has(code) ? [] : ['en']),
      supportedLngs: INTERFACE_LANGUAGE_OPTIONS.map((option) => option.code),
      load: 'currentOnly',
      // Detect and start loading inside init(), so `i18n.language` is set as
      // soon as this runs, as it was when every bundle was inlined.
      initAsync: false,
      // A failed bundle is requested again by the next language switch rather
      // than on a timer that would hold back the first render.
      maxRetries: 0,
      nsSeparator: false, // Allow literal colons in keys (e.g., URLs, labels)
      debug: import.meta.env.DEV,
      interpolation: {
        escapeValue: false, // not needed for react as it escapes by default
      },
      detection: {
        order: ['localStorage', 'navigator'],
        caches: ['localStorage'],
        // Browsers report `zh-CN`/`zh-TW`/`zh`; map them onto our `zhCN`/`zhTW`
        // codes (non-Chinese codes pass through for normal supportedLngs matching).
        convertDetectedLanguage,
      },
    })

  if (instance.hasResourceBundle(instance.language, 'translation')) return
  await instance.reloadResources(['en'], 'translation')
}

const requestedLanguages = new WeakMap<I18n, string>()

/**
 * Switch the interface language once its bundle is loaded.
 *
 * The current language stays on screen while the bundle downloads. When the
 * download fails, nothing changes (including the saved preference) and the
 * result is `failed`; choosing the language again retries the download. When
 * another switch starts before this one finishes, only the latest one applies
 * and the earlier one resolves `superseded`.
 */
export async function changeInterfaceLanguage(
  instance: I18n,
  language: string
): Promise<LanguageChangeResult> {
  requestedLanguages.set(instance, language)
  if (!instance.hasResourceBundle(language, 'translation')) {
    await instance.reloadResources([language], 'translation')
  }
  if (requestedLanguages.get(instance) !== language) return 'superseded'
  requestedLanguages.delete(instance)
  if (!instance.hasResourceBundle(language, 'translation')) return 'failed'
  await instance.changeLanguage(language)
  return 'changed'
}
