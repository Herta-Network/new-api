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

import {
  changeInterfaceLanguage,
  initI18n,
  type LocaleLoaders,
} from '../locale-loader'
import en from '../locales/en.json'
import fr from '../locales/fr.json'
import ja from '../locales/ja.json'
import ru from '../locales/ru.json'
import vi_ from '../locales/vi.json'
import zhTW from '../locales/zh-TW.json'
import zh from '../locales/zh.json'

type LocaleModule = { default: { translation: Record<string, string> } }

// `auth.resetPasswordConfirm.backToLogin` is a key whose English text differs
// from the key, so it shows whether English was really loaded as the fallback.
const translations = {
  en: {
    'Change language': 'Change language',
    'auth.resetPasswordConfirm.backToLogin': 'Return to login',
  },
  zhCN: {
    'Change language': '更改语言',
    'auth.resetPasswordConfirm.backToLogin': '返回登录',
  },
  fr: {
    'Change language': 'Changer de langue',
    'auth.resetPasswordConfirm.backToLogin': 'Retour à la connexion',
  },
  ja: {
    'Change language': '言語を変更',
    'auth.resetPasswordConfirm.backToLogin': 'ログインに戻る',
  },
}

function bundle(translation: Record<string, string>): Promise<LocaleModule> {
  return Promise.resolve({ default: { translation } })
}

function createLoaders() {
  const unused = () => Promise.reject(new Error('not loaded in this test'))
  return {
    en: vi.fn(() => bundle(translations.en)),
    zhCN: vi.fn(() => bundle(translations.zhCN)),
    zhTW: vi.fn(unused),
    fr: vi.fn(() => bundle(translations.fr)),
    ru: vi.fn(unused),
    ja: vi.fn(() => bundle(translations.ja)),
    vi: vi.fn(unused),
  } satisfies LocaleLoaders
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

beforeEach(() => {
  // i18next debug logging would print every loaded bundle.
  vi.stubEnv('DEV', false)
  localStorage.clear()
})

afterEach(() => {
  vi.unstubAllEnvs()
  localStorage.clear()
})

describe('initI18n', () => {
  test('saved language is translated once init resolves and English is not downloaded', async () => {
    localStorage.setItem('i18nextLng', 'zhCN')
    const loaders = createLoaders()
    const instance = i18next.createInstance()

    await initI18n(instance, loaders)

    expect(instance.language).toBe('zhCN')
    expect(instance.t('Change language')).toBe('更改语言')
    expect(loaders.en).not.toHaveBeenCalled()
  })

  test('Chinese browser without a saved choice loads the Simplified Chinese bundle', async () => {
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['zh-CN'])
    const loaders = createLoaders()
    const instance = i18next.createInstance()

    await initI18n(instance, loaders)

    expect(instance.language).toBe('zhCN')
    expect(instance.t('Change language')).toBe('更改语言')
    expect(localStorage.getItem('i18nextLng')).toBe('zhCN')
  })

  test('saved language failing to load shows English and keeps the saved choice', async () => {
    localStorage.setItem('i18nextLng', 'zhCN')
    const loaders = createLoaders()
    loaders.zhCN.mockRejectedValue(new Error('offline'))
    const instance = i18next.createInstance()

    await initI18n(instance, loaders)

    expect(instance.t('auth.resetPasswordConfirm.backToLogin')).toBe(
      'Return to login'
    )
    expect(localStorage.getItem('i18nextLng')).toBe('zhCN')
  })

  test.each([
    ['en', en],
    ['zhCN', zh],
    ['zhTW', zhTW],
    ['fr', fr],
    ['ru', ru],
    ['ja', ja],
    ['vi', vi_],
  ])(
    'default loader for %s puts that locale file in the store',
    async (code, file) => {
      localStorage.setItem('i18nextLng', code)
      const instance = i18next.createInstance()

      await initI18n(instance)

      expect(instance.language).toBe(code)
      expect(instance.getResourceBundle(code, 'translation')).toEqual(
        file.translation
      )
    }
  )
})

describe('changeInterfaceLanguage', () => {
  test('current language stays on screen until the new bundle arrives', async () => {
    localStorage.setItem('i18nextLng', 'zhCN')
    const loaders = createLoaders()
    const instance = i18next.createInstance()
    await initI18n(instance, loaders)
    const frBundle = deferred<LocaleModule>()
    loaders.fr.mockReturnValueOnce(frBundle.promise)

    const change = changeInterfaceLanguage(instance, 'fr')

    expect(loaders.fr).toHaveBeenCalledOnce()
    expect(instance.language).toBe('zhCN')
    expect(instance.t('Change language')).toBe('更改语言')

    frBundle.resolve({ default: { translation: translations.fr } })

    await expect(change).resolves.toBe('changed')
    expect(instance.language).toBe('fr')
    expect(instance.t('Change language')).toBe('Changer de langue')
    expect(localStorage.getItem('i18nextLng')).toBe('fr')
  })

  test('bundle failing to load keeps the current language and the saved choice', async () => {
    localStorage.setItem('i18nextLng', 'zhCN')
    const loaders = createLoaders()
    loaders.fr.mockRejectedValue(new Error('offline'))
    const instance = i18next.createInstance()
    await initI18n(instance, loaders)

    await expect(changeInterfaceLanguage(instance, 'fr')).resolves.toBe(
      'failed'
    )

    expect(instance.language).toBe('zhCN')
    expect(instance.t('Change language')).toBe('更改语言')
    expect(localStorage.getItem('i18nextLng')).toBe('zhCN')
  })

  test('choosing a language again after a failed load downloads it again', async () => {
    localStorage.setItem('i18nextLng', 'zhCN')
    const loaders = createLoaders()
    loaders.fr.mockRejectedValueOnce(new Error('offline'))
    const instance = i18next.createInstance()
    await initI18n(instance, loaders)
    await changeInterfaceLanguage(instance, 'fr')

    await expect(changeInterfaceLanguage(instance, 'fr')).resolves.toBe(
      'changed'
    )

    expect(loaders.fr).toHaveBeenCalledTimes(2)
    expect(instance.t('Change language')).toBe('Changer de langue')
  })

  test('overlapping switches apply only the latest choice', async () => {
    localStorage.setItem('i18nextLng', 'zhCN')
    const loaders = createLoaders()
    const instance = i18next.createInstance()
    await initI18n(instance, loaders)
    const frBundle = deferred<LocaleModule>()
    loaders.fr.mockReturnValueOnce(frBundle.promise)

    const toFrench = changeInterfaceLanguage(instance, 'fr')
    await expect(changeInterfaceLanguage(instance, 'ja')).resolves.toBe(
      'changed'
    )
    frBundle.resolve({ default: { translation: translations.fr } })

    await expect(toFrench).resolves.toBe('superseded')
    expect(instance.language).toBe('ja')
    expect(localStorage.getItem('i18nextLng')).toBe('ja')
  })
})
