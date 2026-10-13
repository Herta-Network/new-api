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
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { PageFooterProvider } from '@/components/layout/components/page-footer'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { UsageLogsProvider } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

const clients: QueryClient[] = []
const footers: HTMLDivElement[] = []
afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  for (const footer of footers.splice(0)) footer.remove()
  useAuthStore.getState().auth.setUser(null)
  localStorage.clear()
})

async function renderLogs(entry = '/usage-logs/task?pageSize=20') {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role: 1 })
  const requests: URL[] = []
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    const request = new URL(url, 'https://example.com')
    if (!request.pathname.startsWith('/api/task/')) {
      return { data: { success: true, data: [] } }
    }
    requests.push(request)
    const second = request.searchParams.get('cursor') === 'second'
    const item = {
      id: second ? 2 : 1,
      task_id: second ? 'second-page-task' : 'first-page-task',
      platform: 'suno',
      user_id: 1,
      channel_id: 0,
      group: 'default',
      quota: 0,
      submit_time: 1791840000,
      status: 'IN_PROGRESS',
      action: 'MUSIC',
      properties: {},
    }
    return {
      data: {
        success: true,
        data: {
          items: [item],
          page: second ? 2 : 1,
          page_size: 20,
          next_cursor: second ? '' : 'second',
          previous_cursor: second ? 'first' : '',
        },
      },
    }
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    component: () => (
      <UsageLogsProvider>
        <UsageLogsTable logCategory='task' />
      </UsageLogsProvider>
    ),
    validateSearch: (search: Record<string, unknown>) => search,
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const footer = document.createElement('div')
  document.body.append(footer)
  footers.push(footer)
  render(
    <QueryClientProvider client={client}>
      <PageFooterProvider container={footer}>
        <RouterProvider router={router} />
      </PageFooterProvider>
    </QueryClientProvider>
  )
  await waitFor(() => expect(requests.length).toBeGreaterThan(0))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeVisible()
  )
  return { requests, router }
}

it('navigates both ways using API cursors and clears them when applying a filter', async () => {
  const user = userEvent.setup()
  const { requests } = await renderLogs()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeEnabled()
  )
  expect(requests[0].searchParams.get('pagination')).toBe('cursor')
  expect(requests[0].searchParams.has('cursor')).toBe(false)
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(requests.at(-1)?.searchParams.get('cursor')).toBe('second')
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to previous page' })
    ).toBeEnabled()
  )
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Go to previous page' }))
  await waitFor(() =>
    expect(requests.at(-1)?.searchParams.get('cursor')).toBe('first')
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to next page' })
    ).toBeEnabled()
  )
  await user.type(screen.getByRole('textbox', { name: 'Task ID' }), 'matched')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(requests.at(-1)?.searchParams.get('task_id')).toBe('matched')
  )
  expect(requests.at(-1)?.searchParams.has('cursor')).toBe(false)
  expect(requests.at(-1)?.searchParams.get('p')).toBe('1')
})

it('restores a cursor page from the URL without querying earlier pages', async () => {
  const { requests } = await renderLogs(
    '/usage-logs/task?pageSize=20&page=2&cursor=second'
  )
  expect(requests[0].searchParams.get('cursor')).toBe('second')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Go to previous page' })
    ).toBeEnabled()
  )
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
})
