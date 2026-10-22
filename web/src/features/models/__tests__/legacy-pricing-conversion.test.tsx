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
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosAdapter } from 'axios'
import { toast } from 'sonner'
import { afterEach, expect, it, vi } from 'vitest'

import type {
  LegacyPricingConversion,
  ModelPricingChange,
} from '@/features/model-pricing/api'
import { api } from '@/lib/http-client'
import { createAppQueryClient } from '@/lib/query-client'
import { useAuthStore } from '@/stores/auth-store'

import { LegacyPricingConversionDialog } from '../components/dialogs/legacy-pricing-conversion-dialog'

const originalAdapter = api.defaults.adapter
let client: QueryClient | undefined

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  client?.clear()
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.setUser(null)
})

function preview(version: string): LegacyPricingConversion[] {
  return [
    {
      model_name: 'ratio-model',
      version,
      configured: { ModelRatio: 1.5, CompletionRatio: 4, CacheRatio: 0.1 },
      effective: {
        ModelRatio: 1.5,
        CompletionRatio: 4,
        CacheRatio: 0.1,
        CreateCacheRatio: 1.25,
        ImageRatio: 1,
      },
      cache_write_mode: 'none',
      expression: 'tier("base", p * 3 + c * 12 + cr * 0.3)',
    },
    {
      model_name: 'request-model',
      version: 'request-v1',
      configured: { ModelPrice: 0.02 },
      effective: { ModelPrice: 0.02 },
      expression: 'tier("request", fixed(0.02))',
    },
    {
      model_name: 'video-model',
      version: 'video-v1',
      configured: { ModelPrice: 1 },
      effective: { ModelPrice: 1 },
      unsupported_reason: 'Video pricing must be converted manually.',
    },
  ]
}

it('previews old prices next to generated expressions, rejects a stale batch, and converts after reloading', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'administrator', role: 100 })
  const previews: string[] = []
  const saves: ModelPricingChange[][] = []
  const adapter: AxiosAdapter = async (config) => {
    const respond = (data: unknown) => ({
      data,
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
    if (config.url === '/api/option/model_pricing/convert_legacy') {
      const version = previews.length === 0 ? 'ratio-v1' : 'ratio-v2'
      previews.push(version)
      return respond({ success: true, data: { entries: preview(version) } })
    }
    if (
      config.method === 'patch' &&
      config.url === '/api/option/model_pricing'
    ) {
      const changes = JSON.parse(String(config.data)).changes
      saves.push(changes)
      if (changes[0].expected_version === 'ratio-v1') {
        throw new AxiosError(
          'Request failed with status code 409',
          'ERR_BAD_REQUEST',
          config,
          undefined,
          {
            data: {
              success: false,
              message:
                'model pricing changed; reload before saving: ratio-model',
            },
            status: 409,
            statusText: 'Conflict',
            headers: {},
            config,
          }
        )
      }
      return respond({ success: true, data: {} })
    }
    return respond({ success: true, data: [] })
  }
  api.defaults.adapter = adapter
  client = createAppQueryClient()
  const onOpenChange = vi.fn()
  const notify = vi.spyOn(toast, 'error')
  render(
    <QueryClientProvider client={client}>
      <LegacyPricingConversionDialog open onOpenChange={onOpenChange} />
    </QueryClientProvider>
  )

  const ratioRow = (await screen.findByText('ratio-model')).closest('tr')
  expect(ratioRow).not.toBeNull()
  const ratio = within(ratioRow as HTMLElement)
  expect(ratio.getByText('Input price').nextElementSibling).toHaveTextContent(
    '$3 / 1M'
  )
  expect(
    ratio.getByText('Completion price').nextElementSibling
  ).toHaveTextContent('$12 / 1M')
  expect(
    ratio.getByText('tier("base", p * 3 + c * 12 + cr * 0.3)')
  ).toBeVisible()
  const videoRow = screen.getByText('video-model').closest('tr')
  expect(videoRow).toHaveTextContent(
    'Video pricing must be converted manually.'
  )
  expect(screen.getByRole('status')).toHaveTextContent(
    'To convert: 2 · Needs manual conversion: 1'
  )

  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Convert (2)' }))
  expect(
    await screen.findByText(
      'Some prices changed after this preview, so nothing was saved. Reload the preview and convert again.'
    )
  ).toBeVisible()
  expect(saves[0]).toEqual([
    {
      model_name: 'ratio-model',
      expected_version: 'ratio-v1',
      pricing: {
        ModelRatio: 1.5,
        CompletionRatio: 4,
        CacheRatio: 0.1,
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr':
          'tier("base", p * 3 + c * 12 + cr * 0.3)',
      },
    },
    {
      model_name: 'request-model',
      expected_version: 'request-v1',
      pricing: {
        ModelPrice: 0.02,
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr': 'tier("request", fixed(0.02))',
      },
    },
  ])
  expect(screen.getByRole('button', { name: 'Convert (2)' })).toBeDisabled()
  expect(onOpenChange).not.toHaveBeenCalled()
  expect(notify).not.toHaveBeenCalled()

  await user.click(screen.getByRole('button', { name: 'Reload preview' }))
  await waitFor(() => expect(previews).toEqual(['ratio-v1', 'ratio-v2']))
  const convert = screen.getByRole('button', { name: 'Convert (2)' })
  await waitFor(() => expect(convert).toBeEnabled())
  await user.click(convert)
  await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  expect(saves).toHaveLength(2)
  expect(saves[1][0].expected_version).toBe('ratio-v2')
})

it('explains that nothing is left to convert', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'administrator', role: 100 })
  api.defaults.adapter = async (config) => ({
    data: { success: true, data: { entries: [] } },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  client = createAppQueryClient()
  render(
    <QueryClientProvider client={client}>
      <LegacyPricingConversionDialog open onOpenChange={() => {}} />
    </QueryClientProvider>
  )
  expect(await screen.findByText('No models use legacy pricing')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Convert (0)' })).toBeDisabled()
})
