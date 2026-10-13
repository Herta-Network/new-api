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
import type { BackendModule, ResourceLanguage } from 'i18next'

const languageLoaders: Record<
  string,
  () => Promise<{ default: ResourceLanguage }>
> = {
  zhCN: () => import(/* webpackChunkName: "locale-zhCN" */ './locales/zh.json'),
  zhTW: () =>
    import(/* webpackChunkName: "locale-zhTW" */ './locales/zh-TW.json'),
  fr: () => import(/* webpackChunkName: "locale-fr" */ './locales/fr.json'),
  ru: () => import(/* webpackChunkName: "locale-ru" */ './locales/ru.json'),
  ja: () => import(/* webpackChunkName: "locale-ja" */ './locales/ja.json'),
  vi: () => import(/* webpackChunkName: "locale-vi" */ './locales/vi.json'),
}

// English is bundled as the offline fallback. i18next requests another bundle
// only when that language is detected or selected, and caches loaded resources.
export const languageBackend = {
  type: 'backend',
  init() {},
  async read(language: string, namespace: string) {
    const load = languageLoaders[language]
    if (namespace !== 'translation' || !load) return {}
    const module = await load()
    return module.default.translation
  },
} satisfies BackendModule
