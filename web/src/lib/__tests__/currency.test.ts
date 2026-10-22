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
import { afterEach, describe, expect, test } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'
import {
  formatBillingCurrencyFromUSD,
  formatExchangeRate,
  formatQuotaWithCurrency,
  getCurrencyLabel,
} from '@/lib/currency'
import { mapStatusDataToConfig } from '@/lib/status-query'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

/** Apply a `/api/status` currency payload, as the status query does. */
function applyStatus(
  siteCurrency: Record<string, unknown>,
  quotaDisplayMode = 'currency'
): void {
  useSystemConfigStore.getState().setConfig(
    mapStatusDataToConfig({
      quota_per_unit: 500000,
      quota_display_mode: quotaDisplayMode,
      site_currency: siteCurrency,
    })
  )
}

const CNY_SITE = { code: 'CNY', symbol: '¥', usd_rate: '7.2' }

afterEach(() => {
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})

describe('site currency formatting', () => {
  test('a USD site shows quota in dollars', () => {
    applyStatus({ code: 'USD', symbol: '$', usd_rate: '1' })
    expect(formatQuotaWithCurrency(5_000_000, { locale: 'en' })).toBe('$10')
    expect(getCurrencyLabel()).toBe('USD')
  })

  test('a CNY site shows quota at the published USD rate', () => {
    applyStatus({ ...CNY_SITE, rate_source: 'apilayer', rate_fetched_at: 1 })
    expect(formatQuotaWithCurrency(5_000_000, { locale: 'en' })).toBe('¥72')
    expect(useSystemConfigStore.getState().config.currency).toMatchObject({
      siteCurrency: 'CNY',
      usdRate: 7.2,
      rateSource: 'apilayer',
      rateFetchedAt: 1,
    })
    expect(getCurrencyLabel()).toBe('CNY')
  })

  test('a site currency without a usable rate shows USD instead of a guessed amount', () => {
    applyStatus({ code: 'CNY', symbol: '¥', usd_rate: '' })
    expect(formatQuotaWithCurrency(5_000_000, { locale: 'en' })).toBe('$10')
    expect(formatBillingCurrencyFromUSD(1, { locale: 'en' })).toBe('$1')
    expect(getCurrencyLabel()).toBe('USD')
  })

  test('tokens only shows raw quota while prices stay in the site currency', () => {
    applyStatus(CNY_SITE, 'tokens')
    expect(
      formatQuotaWithCurrency(5_000_000, { locale: 'en', abbreviate: false })
    ).toBe('5000000')
    expect(formatBillingCurrencyFromUSD(1, { locale: 'en' })).toBe('¥7.2')
    expect(getCurrencyLabel()).toBe('Tokens')
  })

  test.each([
    ['zhCN', '¥7,200.5', '7.24113'],
    ['zhTW', '¥7,200.5', '7.24113'],
    ['en', '¥7,200.5', '7.24113'],
    ['fr', '7 200,5 ¥', '7,24113'],
    ['ru', '7 200,5 ¥', '7,24113'],
    ['ja', '￥7,200.5', '7.24113'],
    ['vi', '7.200,5 ¥', '7,24113'],
  ])(
    'formats a CNY amount and an exchange rate for the %s interface language',
    (language, amount, rate) => {
      applyStatus(CNY_SITE)
      const locale = toIntlLocale(language)
      expect(formatQuotaWithCurrency(500_035_000, { locale })).toBe(amount)
      expect(formatExchangeRate('7.2411296162201303', locale)).toBe(rate)
    }
  )

  test('an unknown interface language falls back to the runtime locale', () => {
    applyStatus(CNY_SITE)
    const locale = toIntlLocale('not a language')
    expect(locale).toBeUndefined()
    expect(formatQuotaWithCurrency(500_035_000, { locale })).toBe(
      formatQuotaWithCurrency(500_035_000)
    )
    expect(formatExchangeRate('', locale)).toBe('-')
  })
})
