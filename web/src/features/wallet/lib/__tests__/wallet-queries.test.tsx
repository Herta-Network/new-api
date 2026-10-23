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
import {
  QueryClientProvider,
  useQuery,
  type QueryClient,
} from '@tanstack/react-query'
import { act, render, renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useUpdateOption } from '@/features/system-settings/hooks/use-update-option'
import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'

import {
  paymentInfoQueryOptions,
  paymentMethodsQueryOptions,
  redemptionInfoQueryOptions,
  subscriptionPlansQueryOptions,
  walletUserQueryOptions,
} from '../wallet-queries'

const MINUTE = 60 * 1000

const RESPONSES: Record<string, unknown> = {
  '/api/user/self': { id: 1, quota: 500 },
  '/api/payment/info': { currency: 'USD', min_topup: 1 },
  '/api/payment/methods': [],
  '/api/user/topup/info': { enable_redemption: true, topup_link: '' },
  '/api/subscription/plans': [],
}

let requests: string[]
let client: QueryClient

function requestCount(url: string): number {
  return requests.filter((request) => request === url).length
}

/** The wallet page's queries for the balance, settings and plans. */
function WalletQueries() {
  useQuery(walletUserQueryOptions)
  useQuery(paymentInfoQueryOptions)
  useQuery(paymentMethodsQueryOptions('topup'))
  useQuery(redemptionInfoQueryOptions)
  useQuery(subscriptionPlansQueryOptions)
  return null
}

function wrapper(props: { children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
}

async function visitWallet() {
  const view = render(<WalletQueries />, { wrapper })
  await waitFor(() => expect(client.isFetching()).toBe(0))
  view.unmount()
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-23T08:00:00Z'))
  requests = []
  client = createAppQueryClient()
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    requests.push(url)
    return { data: { success: true, data: RESPONSES[url] } }
  })
})

afterEach(() => {
  client.clear()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

test('revisiting the wallet a minute later refreshes the balance but reuses settings and plans', async () => {
  await visitWallet()
  vi.setSystemTime(Date.now() + MINUTE)

  await visitWallet()

  expect(requestCount('/api/user/self')).toBe(2)
  for (const url of [
    '/api/payment/info',
    '/api/payment/methods',
    '/api/user/topup/info',
    '/api/subscription/plans',
  ]) {
    expect(requestCount(url)).toBe(1)
  }
})

test('revisiting the wallet after five minutes refreshes settings and plans', async () => {
  await visitWallet()
  vi.setSystemTime(Date.now() + 5 * MINUTE + 1)

  await visitWallet()

  expect(requestCount('/api/payment/info')).toBe(2)
  expect(requestCount('/api/subscription/plans')).toBe(2)
})

test('saving a payment setting refreshes the cached payment info within its window', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
  await visitWallet()
  const { result } = renderHook(() => useUpdateOption(), { wrapper })

  await act(() =>
    result.current.mutateAsync({ key: 'payment_setting.min_topup', value: '5' })
  )
  await visitWallet()

  expect(requestCount('/api/payment/info')).toBe(2)
  expect(requestCount('/api/user/topup/info')).toBe(2)
})
