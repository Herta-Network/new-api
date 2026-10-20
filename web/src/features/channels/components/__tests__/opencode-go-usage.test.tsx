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

import type { OpenCodeGoUsage } from '../../api'
import { CHANNEL_TYPE_OPENCODE_GO } from '../../constants'
import { channelSchema } from '../../types'
import { ChannelRowActionsLayoutContext } from '../channel-row-actions-context'
import { BalanceCell } from '../channels-columns'
import { ChannelsProvider } from '../channels-provider'
import { OpenCodeGoUsageDialog } from '../dialogs/opencode-go-usage-dialog'

const USAGE_URL = '/api/channel/42/opencode-go/usage'

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

function usageFixture(): OpenCodeGoUsage {
  return {
    five_hour: { status: 'ok', used_percent: 12, limit_window_seconds: 18000 },
    weekly: { status: 'ok', used_percent: 40, limit_window_seconds: 604800 },
    monthly: { status: 'ok', used_percent: 63, limit_window_seconds: 2592000 },
    limit_reached: false,
  }
}

function renderBalanceCell() {
  const channel = channelSchema.parse({
    id: 42,
    type: CHANNEL_TYPE_OPENCODE_GO,
    key: '',
    name: 'Go plan',
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

it('opens OpenCode Go usage from the balance cell with the keyboard instead of updating the balance', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === USAGE_URL) {
      return { data: { success: true, data: usageFixture() } }
    }
    return { data: { success: true, data: [] } }
  })
  const user = userEvent.setup()
  renderBalanceCell()

  const entry = screen.getByRole('button', { name: 'Usage' })
  expect(entry).toHaveAttribute('aria-haspopup', 'dialog')
  entry.focus()
  await user.keyboard('{Enter}')

  const dialog = await screen.findByRole('dialog', {
    name: 'OpenCode Go Usage',
  })
  expect(within(dialog).getByText('Go plan (#42)')).toBeInTheDocument()
  expect(within(dialog).getByText('Available')).toBeInTheDocument()
  expect(
    within(dialog).getByRole('progressbar', { name: '5-Hour Window usage: 12%' })
  ).toBeInTheDocument()
  expect(
    within(dialog).getByRole('progressbar', { name: 'Weekly Window usage: 40%' })
  ).toBeInTheDocument()
  expect(
    within(dialog).getByRole('progressbar', {
      name: 'Monthly Window usage: 63%',
    })
  ).toBeInTheDocument()
  expect(get.mock.calls.some(([url]) => url.includes('update_balance'))).toBe(
    false
  )
})

it('shows the translated server reason and keeps the dialog closed when the key has no Go subscription', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: false,
      message: 'This API key has no OpenCode Go subscription',
      message_key: 'This API key has no OpenCode Go subscription',
    },
  })
  const user = userEvent.setup()
  renderBalanceCell()

  await user.click(screen.getByRole('button', { name: 'Usage' }))

  expect(
    await screen.findByText('This API key has no OpenCode Go subscription')
  ).toBeVisible()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('marks a reached limit and disables refresh while a refresh is running', async () => {
  const onRefresh = vi.fn()
  const usage = { ...usageFixture(), limit_reached: true }
  const user = userEvent.setup()
  const { rerender } = render(
    <OpenCodeGoUsageDialog
      open
      onOpenChange={vi.fn()}
      channelLabel='Go plan (#42)'
      usage={usage}
      onRefresh={onRefresh}
      isRefreshing={false}
    />
  )

  const dialog = screen.getByRole('dialog', { name: 'OpenCode Go Usage' })
  expect(within(dialog).getByText('Limited')).toBeInTheDocument()
  await user.click(within(dialog).getByRole('button', { name: 'Refresh' }))
  expect(onRefresh).toHaveBeenCalledTimes(1)

  rerender(
    <OpenCodeGoUsageDialog
      open
      onOpenChange={vi.fn()}
      channelLabel='Go plan (#42)'
      usage={usage}
      onRefresh={onRefresh}
      isRefreshing
    />
  )
  expect(
    within(screen.getByRole('dialog')).getByRole('button', { name: 'Refresh' })
  ).toBeDisabled()
})
