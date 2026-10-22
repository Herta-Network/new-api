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
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosAdapter } from 'axios'
import { afterEach, expect, it } from 'vitest'

import { api } from '@/lib/http-client'
import { createAppQueryClient } from '@/lib/query-client'
import { useAuthStore } from '@/stores/auth-store'
import { usePricingPreferencesStore } from '@/stores/pricing-preferences-store'

import type { ModelPricingChange, ModelPricingEntry } from '../api'
import { ModelPricingPanel } from '../model-pricing-panel'
import { pricingOptions } from '../pricing'

const originalAdapter = api.defaults.adapter
let client: QueryClient | undefined

afterEach(() => {
  cleanup()
  client?.clear()
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.setUser(null)
})

function expressionEntry(
  name: string,
  expression: string,
  extra: Partial<ModelPricingEntry> = {}
): ModelPricingEntry {
  const values = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': expression,
  }
  return {
    model_name: name,
    version: 'v1',
    configured: values,
    effective: values,
    pricing_mode: 'expression',
    cache_write_mode: 'none',
    ...extra,
  }
}

function renderPanel(entry: ModelPricingEntry) {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'administrator', role: 100 })
  usePricingPreferencesStore.setState({ currency: 'USD' })
  const saved: ModelPricingChange[][] = []
  const adapter: AxiosAdapter = async (config) => {
    const respond = (data: unknown) => ({
      data,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
    if (config.method === 'get' && config.url === '/api/option/model_pricing') {
      return respond({
        success: true,
        data: {
          entries: [entry],
          options: pricingOptions({}),
          empty_version: 'empty',
        },
      })
    }
    if (config.method === 'patch') {
      saved.push(JSON.parse(String(config.data)).changes)
      return respond({ success: true, data: {} })
    }
    if (config.method === 'post') {
      return respond({ success: true, data: { effective: entry.effective } })
    }
    return respond({ success: true, data: [], vendors: [] })
  }
  api.defaults.adapter = adapter
  client = createAppQueryClient()
  render(
    <QueryClientProvider client={client}>
      <ModelPricingPanel modelName={entry.model_name} />
    </QueryClientProvider>
  )
  return { saved }
}

it('edits simple USD prices and saves the canonical expression', async () => {
  const { saved } = renderPanel(
    expressionEntry(
      'simple-model',
      'tier("base", p * 2.5 + c * 15 + cr * 0.25)'
    )
  )
  const output = await screen.findByRole('textbox', { name: 'Output price' })
  expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue(
    '2.5'
  )
  expect(output).toHaveValue('15')
  expect(screen.getByRole('textbox', { name: 'Cache read price' })).toHaveValue(
    '0.25'
  )
  expect(
    screen.getByRole('switch', { name: 'Advanced editor' })
  ).not.toBeChecked()
  const user = userEvent.setup()
  await user.clear(output)
  await user.type(output, '10')
  // A cache read priced like the input is billed as input.
  const cache = screen.getByRole('textbox', { name: 'Cache read price' })
  await user.clear(cache)
  await user.type(cache, '2.5')
  await user.click(screen.getByRole('button', { name: 'Save model prices' }))
  await waitFor(() => expect(saved).toHaveLength(1))
  expect(saved[0]).toEqual([
    {
      model_name: 'simple-model',
      expected_version: 'v1',
      pricing: {
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr': 'tier("base", p * 2.5 + c * 10)',
      },
      reset: false,
    },
  ])
})

it('switches to the advanced editor and back without changing the expression', async () => {
  const expression = 'tier("base", p * 3 + c * 15 + cr * 0.3)'
  const { saved } = renderPanel(expressionEntry('round-trip', expression))
  const user = userEvent.setup()
  const advanced = await screen.findByRole('switch', {
    name: 'Advanced editor',
  })
  await user.click(advanced)
  expect(advanced).toBeChecked()
  expect(screen.getByRole('combobox', { name: 'Editor mode' })).toBeVisible()
  expect(
    screen.queryByRole('group', { name: 'Billing unit' })
  ).not.toBeInTheDocument()
  await user.click(advanced)
  expect(advanced).not.toBeChecked()
  expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue('3')
  await user.click(screen.getByRole('button', { name: 'Save model prices' }))
  await waitFor(() => expect(saved).toHaveLength(1))
  expect(saved[0][0].pricing['billing_setting.billing_expr']).toBe(expression)
})

it.each([
  {
    name: 'context tiers',
    expression:
      'len <= 200000 ? tier("standard", p * 3 + c * 15) : tier("long", p * 6 + c * 22.5)',
    reason:
      'Simple prices cannot show this expression without changing what it bills. Keep editing it in the advanced editor.',
  },
  {
    name: 'request rules',
    expression:
      '(tier("base", p * 5 + c * 25)) * (header("anthropic-beta") == "fast-mode" ? 6 : 1)',
    reason:
      'Request rules need the advanced editor. Remove them to use simple prices.',
  },
])(
  'opens $name in the advanced editor and saves them unchanged',
  async ({ expression, reason }) => {
    const { saved } = renderPanel(expressionEntry('advanced-model', expression))
    const user = userEvent.setup()
    const advanced = await screen.findByRole('switch', {
      name: 'Advanced editor',
    })
    expect(advanced).toBeChecked()
    await user.click(advanced)
    expect(advanced).toBeChecked()
    expect(screen.getByRole('status')).toHaveTextContent(reason)
    await user.click(screen.getByRole('button', { name: 'Save model prices' }))
    await waitFor(() => expect(saved).toHaveLength(1))
    expect(saved[0][0].pricing['billing_setting.billing_expr']).toBe(expression)
  }
)

it('requires a 1-hour price next to a Claude 5-minute cache write price', async () => {
  const { saved } = renderPanel({
    model_name: 'claude-custom',
    version: 'empty',
    configured: {},
    effective: {},
    pricing_mode: 'unpriced',
    cache_write_mode: 'claude_ttl',
  })
  const user = userEvent.setup()
  const save = await screen.findByRole('button', { name: 'Save model prices' })
  // An unpriced model starts empty and cannot be saved as free.
  expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue('')
  await user.click(save)
  expect(screen.getByRole('textbox', { name: 'Input price' })).toBeInvalid()
  await user.type(screen.getByRole('textbox', { name: 'Input price' }), '3')
  await user.type(screen.getByRole('textbox', { name: 'Output price' }), '15')
  await user.type(
    screen.getByRole('textbox', { name: 'Cache write price (5 min)' }),
    '3.75'
  )
  const hour = screen.getByRole('textbox', {
    name: 'Cache write price (1 hour)',
  })
  expect(hour).toBeRequired()
  expect(
    screen.getByText('Enter the 1-hour cache write price as well.')
  ).toBeVisible()
  await user.click(save)
  expect(saved).toHaveLength(0)
  await user.type(hour, '6')
  await user.click(save)
  await waitFor(() => expect(saved).toHaveLength(1))
  expect(saved[0][0]).toMatchObject({
    expected_version: 'empty',
    pricing: {
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr':
        'tier("base", p * 3 + c * 15 + cc * 3.75 + cc1h * 6)',
    },
  })
})

it('writes a fixed per-request price', async () => {
  const { saved } = renderPanel({
    model_name: 'search-tool',
    version: 'empty',
    configured: {},
    effective: {},
    pricing_mode: 'unpriced',
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: 'Per request' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Price per request' }),
    '0.002'
  )
  await user.click(screen.getByRole('button', { name: 'Save model prices' }))
  await waitFor(() => expect(saved).toHaveLength(1))
  expect(saved[0][0].pricing['billing_setting.billing_expr']).toBe(
    'tier("request", fixed(0.002))'
  )
})

it.each([
  {
    state: 'converted',
    configured: {
      ModelRatio: 1.5,
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr': 'tier("base", p * 3 + c * 3)',
    },
    pricingMode: 'expression' as const,
    legacyTabs: false,
  },
  {
    state: 'legacy',
    configured: { ModelRatio: 1.5 },
    pricingMode: 'legacy' as const,
    legacyTabs: true,
  },
])(
  'shows the deprecated legacy editors only for a $state model',
  async ({ configured, pricingMode, legacyTabs }) => {
    renderPanel({
      model_name: 'mode-model',
      version: 'v1',
      configured,
      effective: { ...configured, CompletionRatio: 1 },
      pricing_mode: pricingMode,
    })
    await screen.findByRole('button', { name: 'Save model prices' })
    if (legacyTabs) {
      expect(
        screen.getByRole('tab', { name: 'Per-token (deprecated)' })
      ).toHaveAttribute('aria-selected', 'true')
      expect(
        screen.getByRole('button', { name: 'Convert to expression' })
      ).toBeVisible()
      return
    }
    expect(
      screen.queryByRole('tab', { name: 'Per-token (deprecated)' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Input price' })).toHaveValue(
      '3'
    )
  }
)
