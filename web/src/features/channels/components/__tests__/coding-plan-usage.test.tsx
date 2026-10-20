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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Toaster, toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { CodingPlanUsage } from '../../api'
import { CHANNEL_TYPE_MOONSHOT, CHANNEL_TYPE_ZHIPU_V4 } from '../../constants'
import { channelSchema } from '../../types'
import { ChannelRowActionsLayoutContext } from '../channel-row-actions-context'
import { BalanceCell } from '../channels-columns'
import { ChannelsProvider } from '../channels-provider'

const USAGE_URL = '/api/channel/42/coding-plan/usage'

let client: QueryClient

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})

afterEach(() => {
  cleanup()
  toast.dismiss()
  client.clear()
  vi.restoreAllMocks()
})

function glmUsageFixture(): CodingPlanUsage {
  return {
    provider: 'glm',
    plan: 'pro',
    five_hour: { used_percent: 12.5, limit_window_seconds: 18000 },
    weekly: { used_percent: 100, limit_window_seconds: 604800 },
    limit_reached: true,
  }
}

function renderBalanceCell(type: number, baseUrl: string) {
  const channel = channelSchema.parse({
    id: 42,
    type,
    key: '',
    name: 'Plan channel',
    base_url: baseUrl,
    status: 1,
    created_time: 1,
    test_time: 0,
    response_time: 0,
    balance_updated_time: 0,
  })
  render(
    <QueryClientProvider client={client}>
      <ChannelsProvider>
        <ChannelRowActionsLayoutContext.Provider value='table'>
          <BalanceCell channel={channel} />
        </ChannelRowActionsLayoutContext.Provider>
      </ChannelsProvider>
      <Toaster />
    </QueryClientProvider>
  )
}

it('opens GLM Coding Plan usage from the balance cell with the keyboard instead of updating the balance', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === USAGE_URL) {
      return { data: { success: true, data: glmUsageFixture() } }
    }
    return { data: { success: true, data: [] } }
  })
  const user = userEvent.setup()
  renderBalanceCell(CHANNEL_TYPE_ZHIPU_V4, 'glm-coding-plan')

  const entry = screen.getByRole('button', { name: 'Usage' })
  expect(entry).toHaveAttribute('aria-haspopup', 'dialog')
  entry.focus()
  await user.keyboard('{Enter}')

  const dialog = await screen.findByRole('dialog', {
    name: 'GLM Coding Plan Usage',
  })
  expect(within(dialog).getByText('Plan channel (#42)')).toBeInTheDocument()
  expect(within(dialog).getByText('Limited')).toBeInTheDocument()
  expect(within(dialog).getByText('Plan: pro')).toBeInTheDocument()
  expect(
    within(dialog).getByRole('progressbar', {
      name: '5-Hour Window usage: 12.5%',
    })
  ).toBeInTheDocument()
  expect(
    within(dialog).getByRole('progressbar', {
      name: 'Weekly Window usage: 100%',
    })
  ).toBeInTheDocument()
  expect(within(dialog).queryByText('Monthly Window')).not.toBeInTheDocument()
  expect(get.mock.calls.some(([url]) => url.includes('update_balance'))).toBe(
    false
  )
})

it('shows the translated server reason and keeps the dialog closed when the plan rejects the key', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: false,
      message: 'Kimi For Coding rejected the API key',
      message_key: '{{provider}} rejected the API key',
      message_params: { provider: 'Kimi For Coding' },
    },
  })
  const user = userEvent.setup()
  renderBalanceCell(CHANNEL_TYPE_MOONSHOT, 'https://api.kimi.com/coding')

  await user.click(screen.getByRole('button', { name: 'Usage' }))

  expect(
    await screen.findByText('Kimi For Coding rejected the API key')
  ).toBeVisible()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('keeps the balance badge of a Moonshot open platform channel', () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
  renderBalanceCell(CHANNEL_TYPE_MOONSHOT, 'https://api.moonshot.cn')

  expect(
    screen.queryByRole('button', { name: 'Usage' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Usage')).not.toBeInTheDocument()
})
