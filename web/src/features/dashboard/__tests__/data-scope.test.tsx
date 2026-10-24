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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'

import { ThemeProvider } from '@/context/theme-provider'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import type { DataScope } from '@/lib/workspace'
import { useAuthStore } from '@/stores/auth-store'

import { getFlowQuotaDates, getUserQuotaDates } from '../api'
import { AdminOverview } from '../components/admin-overview/admin-overview'
import { LogStatCards } from '../components/models/log-stat-cards'
import type {
  ConsoleOverview,
  ConsoleRankings,
  ConsoleRevenue,
  ConsoleRevenuePeriod,
  ConsoleUserPeriod,
  ConsoleUsers,
} from '../types'

const chart = vi.hoisted(() =>
  vi.fn(
    (_props: {
      spec: { type: string; data: { values: Record<string, unknown>[] }[] }
    }) => null
  )
)
vi.mock('@visactor/react-vchart', () => ({ VChart: chart }))
vi.mock('@visactor/vchart', () => ({
  ThemeManager: { setCurrentTheme: vi.fn() },
}))

const range = { start_timestamp: 1788600000, end_timestamp: 1788686400 }

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  chart.mockClear()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

function mockQuotaData() {
  return vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: [] } })
}

describe('dashboard data scope', () => {
  it.each<[DataScope, string, string]>([
    ['self', '/api/data/self', '/api/data/flow/self'],
    ['site', '/api/data', '/api/data/flow'],
  ])(
    'the %s scope reads quota data from %s and flow data from %s',
    async (scope, quotaPath, flowPath) => {
      const get = mockQuotaData()

      await getUserQuotaDates(range, scope)
      await getFlowQuotaDates(range, scope)

      expect(get).toHaveBeenNthCalledWith(1, quotaPath, { params: range })
      expect(get).toHaveBeenNthCalledWith(2, flowPath, { params: range })
    }
  )

  it.each<[DataScope, string]>([
    ['self', '/api/data/self'],
    ['site', '/api/data'],
  ])('an administrator sees %s statistics from %s', async (scope, path) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    const get = mockQuotaData()

    render(<LogStatCards scope={scope} />)

    await waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    expect(get.mock.calls[0][0]).toBe(path)
  })
})

const zone = { time_zone: 'Asia/Shanghai', utc_offset: 28800 }

function revenuePeriod(
  total: number,
  extra: Partial<ConsoleRevenuePeriod> = {}
): ConsoleRevenuePeriod {
  return {
    total,
    topup: total - 1000,
    subscription: 1000,
    orders: 1,
    manual: 0,
    manual_orders: 0,
    balance_spending: 0,
    balance_orders: 0,
    other_currencies: [],
    list: [
      { date: '2026-10-21', start: 1, total: 0, topup: 0, subscription: 0 },
      {
        date: '2026-10-22',
        start: 2,
        total,
        topup: total - 1000,
        subscription: 1000,
      },
    ],
    ...extra,
  }
}

const userPeriod: ConsoleUserPeriod = {
  registrations: 3,
  first_time_payers: 2,
  returning_payers: 1,
  list: [
    {
      date: '2026-10-22',
      start: 2,
      registrations: 3,
      first_time_payers: 2,
      returning_payers: 1,
    },
  ],
}

const consoleData: Record<string, unknown> = {
  '/api/admin/console/overview': {
    ...zone,
    usage_data_enabled: true,
    requests: 1234,
    quota: 9000,
    tokens: 900,
    active_users: 2,
    registrations: 2,
    currency: 'CNY',
    revenue: 12000,
    revenue_orders: 4,
    other_revenue: [],
    channels: { total: 6, enabled: 3, manually_disabled: 1, auto_disabled: 2 },
    attention: {
      count: 3,
      unfulfilled_orders: 1,
      unmatched_payments: 2,
      amounts: [
        { currency: 'CNY', amount: 8000 },
        { currency: 'USD', amount: 1999 },
      ],
    },
  } satisfies ConsoleOverview,
  '/api/admin/console/revenue': {
    ...zone,
    currency: 'CNY',
    today: revenuePeriod(12000),
    monthly: revenuePeriod(14000, { manual: 1500, manual_orders: 1 }),
    all: revenuePeriod(24000),
  } satisfies ConsoleRevenue,
  '/api/admin/console/users': {
    ...zone,
    today: userPeriod,
    monthly: userPeriod,
    all: userPeriod,
  } satisfies ConsoleUsers,
  '/api/admin/console/rankings': {
    ...zone,
    period: 'today',
    start: 1,
    end: 2,
    models: [{ model_name: 'gpt-b', quota: 6000, requests: 3, tokens: 600 }],
    users: [],
  } satisfies ConsoleRankings,
}

// As in the app, interpolated values are not HTML-escaped.
const i18n = createInstance()
beforeAll(async () => {
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
})

async function renderAdminOverview() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const root = createRootRoute({ component: () => <Outlet /> })
  const pages = [
    '/admin/dashboard/$section',
    '/admin/users',
    '/admin/channels',
    '/admin/payment-orders/$section',
  ].map((path) => createRoute({ getParentRoute: () => root, path }))
  const router = createRouter({
    routeTree: root.addChildren([
      createRoute({
        getParentRoute: () => root,
        path: '/admin',
        component: AdminOverview,
      }),
      ...pages,
    ]),
    history: createMemoryHistory({ initialEntries: ['/admin'] }),
  })
  await router.load()
  render(
    <I18nextProvider i18n={i18n}>
      <ThemeProvider>
        <QueryClientProvider client={client}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </ThemeProvider>
    </I18nextProvider>
  )
}

function linkTarget(link: HTMLElement) {
  const url = new URL(link.getAttribute('href') ?? '', 'http://localhost')
  return {
    path: url.pathname,
    search: Object.fromEntries(
      [...url.searchParams].map(([key, value]) => [key, JSON.parse(value)])
    ),
  }
}

describe('admin operations overview', () => {
  it('reads the site console endpoints and links every figure to its details', async () => {
    const get = vi
      .spyOn(api, 'get')
      .mockImplementation(async (url: string) => ({
        data: { success: true, data: consoleData[url] },
      }))

    await renderAdminOverview()

    const revenueLink = await screen.findByRole('link', {
      name: /Revenue today.*CN¥120\.00/,
    })
    expect(linkTarget(revenueLink).path).toBe('/admin/payment-orders/orders')
    expect(
      linkTarget(screen.getByRole('link', { name: /Requests today.*1,234/ }))
        .path
    ).toBe('/admin/dashboard/models')
    expect(
      linkTarget(
        screen.getByRole('link', {
          name: /Enabled channels.*6 in total, 2 disabled automatically/,
        })
      ).path
    ).toBe('/admin/channels')
    expect(
      linkTarget(screen.getByRole('link', { name: 'Unfulfilled orders: 1' }))
    ).toEqual({
      path: '/admin/payment-orders/orders',
      search: { status: ['2'] },
    })
    expect(
      linkTarget(screen.getByRole('link', { name: 'Unmatched payments: 2' }))
    ).toEqual({
      path: '/admin/payment-orders/unmatched',
      search: { uState: ['false'] },
    })
    expect(
      screen.getByText('CN¥80.00 + $19.99 waiting for an operator')
    ).toBeVisible()
    expect(
      screen.getByText(/server time zone Asia\/Shanghai \(UTC\+08:00\)/)
    ).toBeVisible()

    expect(get.mock.calls.map((call) => call[0]).sort()).toEqual([
      '/api/admin/console/overview',
      '/api/admin/console/rankings',
      '/api/admin/console/revenue',
      '/api/admin/console/users',
    ])
    expect(get).toHaveBeenCalledWith('/api/admin/console/rankings', {
      params: { period: 'today' },
    })
    // An empty ranking shows its empty state instead of a chart.
    expect(await screen.findByText('No usage in this period')).toBeVisible()
    await waitFor(() =>
      expect(
        chart.mock.calls.some((call) =>
          call[0].spec.data[0].values.some(
            (value) => value.name === 'gpt-b' && value.quota === 6000
          )
        )
      ).toBe(true)
    )
  })

  it('switching the revenue period shows that period in the summary strip', async () => {
    const user = userEvent.setup()
    vi.spyOn(api, 'get').mockImplementation(async (url: string) => ({
      data: { success: true, data: consoleData[url] },
    }))

    await renderAdminOverview()

    expect(await screen.findByText('CN¥140.00')).toBeVisible()
    expect(
      screen.getByText('Includes CN¥15.00 completed by hand')
    ).toBeVisible()

    const periods = screen.getByRole('tablist', { name: 'Revenue' })
    await user.click(within(periods).getByRole('tab', { name: 'All time' }))

    expect(
      within(periods).getByRole('tab', { name: 'All time' })
    ).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByText('CN¥240.00')).toBeVisible()
    expect(screen.queryByText('CN¥140.00')).not.toBeInTheDocument()
    expect(
      screen.queryByText('Includes CN¥15.00 completed by hand')
    ).not.toBeInTheDocument()
  })

  it('revenue today shows revenue in a currency without an exchange rate apart', async () => {
    const overview = {
      ...(consoleData['/api/admin/console/overview'] as ConsoleOverview),
      currency: 'USD',
      revenue: 0,
      revenue_orders: 0,
      other_revenue: [{ currency: 'CNY', amount: 12000 }],
    } satisfies ConsoleOverview
    vi.spyOn(api, 'get').mockImplementation(async (url: string) => ({
      data: {
        success: true,
        data:
          url === '/api/admin/console/overview' ? overview : consoleData[url],
      },
    }))

    await renderAdminOverview()

    expect(
      await screen.findByRole('link', {
        name: /Revenue today.*\$0\.00.*Other currencies, not added in: CN¥120\.00/,
      })
    ).toBeVisible()
  })

  it('shows the empty state when no overview figure loads', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('offline'))

    await renderAdminOverview()

    expect(await screen.findByText('No overview data yet')).toBeVisible()
    expect(screen.queryByText('Revenue today')).not.toBeInTheDocument()
  })
})
