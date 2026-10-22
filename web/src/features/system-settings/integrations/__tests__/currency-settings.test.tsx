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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ComponentProps } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { CurrencySettingsSection } from '../currency-settings-section'
import type { ExchangeRatePair } from '../exchange-rates-api'

type CurrencyValues = ComponentProps<
  typeof CurrencySettingsSection
>['defaultValues']

const defaults: CurrencyValues = {
  currency: 'CNY',
  quotaDisplayMode: 'currency',
  overrides: '{}',
  billingApiUsesTokenQuota: true,
  exchangeRateKeyConfigured: false,
  quotaPerUnit: 500000,
}

const fetchedAt = Math.floor(Date.now() / 1000)

function quotaPair(
  quote: string,
  overrides: Partial<ExchangeRatePair> = {}
): ExchangeRatePair {
  return {
    base: 'USD',
    quote,
    uses: ['quota'],
    live: { rate: '7.2411', fetched_at: fetchedAt },
    override: '',
    status: 'live',
    ...overrides,
  }
}

/** Serve the rate overview per previewed site currency. */
function mockApi(pairsByCurrency: Record<string, ExchangeRatePair[]>) {
  const get = vi
    .spyOn(api, 'get')
    .mockImplementation(async (url: string, config?: unknown) => {
      if (url === '/api/payment/settings/platforms') {
        return {
          data: {
            success: true,
            data: {
              currencies: ['USD', 'CNY', 'EUR'],
              platforms: [],
              exchange_rate_api_key_configured: false,
            },
          },
        }
      }
      const currency = (config as { params: { currency: string } }).params
        .currency
      return {
        data: {
          success: true,
          data: { currency, pairs: pairsByCurrency[currency] ?? [] },
        },
      }
    })
  return {
    get,
    put: vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } }),
    post: vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: { success: true, data: { pairs: [] } } }),
  }
}

function CurrencyFixture(props: { values: CurrencyValues }) {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider actionsContainer={actions}>
      <div ref={setActions} />
      <CurrencySettingsSection defaultValues={props.values} />
    </SettingsPageProvider>
  )
}

function renderSection(values: Partial<CurrencyValues> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <CurrencyFixture values={{ ...defaults, ...values }} />
    </QueryClientProvider>
  )
  return userEvent.setup()
}

function rateRow(name: string): HTMLElement {
  const row = screen.getByText(name).closest('tr')
  if (!row) throw new Error(`no row for ${name}`)
  return row
}

afterEach(() => vi.restoreAllMocks())

describe('currency and exchange rate settings', () => {
  it('shows the live rate amounts use and saves a pinned rate as an override of the pair', async () => {
    const request = mockApi({ CNY: [quotaPair('CNY')] })
    const user = renderSection()

    expect(
      await screen.findByText(
        /Amounts are shown in CNY at the live rate 1 USD = 7\.2411 CNY/
      )
    ).toBeVisible()
    const row = rateRow('1 USD = ? CNY')
    expect(within(row).getByText('Live')).toBeVisible()

    await user.type(
      within(row).getByRole('textbox', { name: 'Rate you set for USD to CNY' }),
      '7.3'
    )
    expect(within(row).getByText('Overridden')).toBeVisible()
    expect(
      screen.getByText(/at the rate you set: 1 USD = 7\.3 CNY/)
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() =>
      expect(request.put).toHaveBeenCalledWith('/api/option/', {
        key: 'payment_setting.exchange_rate_overrides',
        value: '{"USD/CNY":"7.3"}',
      })
    )
    expect(request.put).toHaveBeenCalledTimes(1)
    expect(request.post).not.toHaveBeenCalled()
  })

  it('edits an override saved for the reverse pair in its own direction and removes it when cleared', async () => {
    const request = mockApi({
      CNY: [quotaPair('CNY', { override: '7.1428571428571429' })],
    })
    const user = renderSection({ overrides: '{"CNY/USD":"0.14"}' })

    const input = await screen.findByRole('textbox', {
      name: 'Rate you set for CNY to USD',
    })
    expect(input).toHaveValue('0.14')
    expect(screen.getByText('1 CNY = ? USD')).toBeVisible()
    await user.clear(input)
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() =>
      expect(request.put).toHaveBeenCalledWith('/api/option/', {
        key: 'payment_setting.exchange_rate_overrides',
        value: '{}',
      })
    )
  })

  it('warns that amounts fall back to USD while the site currency has no rate', async () => {
    mockApi({
      CNY: [quotaPair('CNY', { live: null, status: 'missing' })],
    })
    renderSection()

    expect(
      await screen.findByText(
        /There is no usable rate for CNY: amounts are shown in USD/
      )
    ).toBeVisible()
    expect(within(rateRow('1 USD = ? CNY')).getByText('Missing')).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Refresh live rates' })
    ).toBeDisabled()
  })

  it('rejects a rate that is not a positive decimal', async () => {
    const request = mockApi({ CNY: [quotaPair('CNY')] })
    const user = renderSection()

    await user.type(
      await screen.findByRole('textbox', {
        name: 'Rate you set for USD to CNY',
      }),
      '-7'
    )
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))

    expect(
      await screen.findByText(
        'Enter a positive rate with at most 12 decimal places'
      )
    ).toBeVisible()
    expect(request.put).not.toHaveBeenCalled()
  })

  it('previews the pairs of a new site currency and fetches its live rates after saving', async () => {
    const request = mockApi({
      CNY: [quotaPair('CNY')],
      EUR: [quotaPair('EUR', { live: null, status: 'missing' })],
    })
    const user = renderSection({ exchangeRateKeyConfigured: true })

    await user.click(
      await screen.findByRole('combobox', { name: 'Site currency' })
    )
    await user.click(await screen.findByRole('option', { name: 'EUR' }))
    expect(await screen.findByText('1 USD = ? EUR')).toBeVisible()
    expect(request.get).toHaveBeenCalledWith('/api/payment/settings/rates', {
      params: { currency: 'EUR' },
    })
    await user.click(screen.getByRole('button', { name: 'Save Changes' }))

    await waitFor(() =>
      expect(request.post).toHaveBeenCalledWith(
        '/api/payment/settings/rates/refresh'
      )
    )
    expect(request.put).toHaveBeenCalledWith('/api/option/', {
      key: 'payment_setting.currency',
      value: 'EUR',
    })
  })
})
