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
  createMemoryHistory,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  Link,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { createAppQueryClient } from '@/lib/query-client'
import { Route as KeysRoute } from '@/routes/_authenticated/user/keys/index'

import { apiKeySchema } from '../../types'
import { ApiKeysProvider } from '../api-keys-provider'
import { ApiKeysTable } from '../api-keys-table'

const key = apiKeySchema.parse({
  id: 7,
  name: 'production',
  key: 'demo********1234',
  status: 1,
  remain_quota: 40_000_000,
  used_quota: 60_000_000,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 0,
  accessed_time: 0,
  group: 'default',
  model_limits_enabled: false,
})
const keysPage = { success: true, data: { items: [key], total: 1 } }

const i18n = createInstance()
await i18n.init({
  lng: 'en',
  resources: { en: { translation: {} } },
  initAsync: false,
})

const clients: QueryClient[] = []

function KeysPage() {
  return (
    <ApiKeysProvider>
      <ApiKeysTable />
    </ApiKeysProvider>
  )
}

/**
 * The app's keys route (its search schema, loader deps and loader) under a
 * home page that links to it, with the app's preload settings.
 */
function renderApp(queryClient: QueryClient) {
  const root = createRootRouteWithContext<{ queryClient: QueryClient }>()()
  const home = createRoute({
    getParentRoute: () => root,
    path: '/',
    component: () => <Link to='/user/keys'>API Keys</Link>,
  })
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  // The app route's options are typed against the app's route tree.
  const keysOptions = KeysRoute.options as unknown as {
    validateSearch: (search: Record<string, unknown>) => Record<string, unknown>
    loaderDeps: (args: {
      search: Record<string, unknown>
    }) => Record<string, unknown>
    loader: (args: {
      context: { queryClient: QueryClient }
      deps: Record<string, unknown>
    }) => void
  }
  const keys = createRoute({
    getParentRoute: () => auth,
    path: 'user/keys/',
    validateSearch: keysOptions.validateSearch,
    loaderDeps: keysOptions.loaderDeps,
    loader: keysOptions.loader,
    component: KeysPage,
  })
  const router = createRouter({
    routeTree: root.addChildren([home, auth.addChildren([keys])]),
    context: { queryClient },
    history: createMemoryHistory({ initialEntries: ['/'] }),
    defaultPreload: 'intent',
    defaultPreloadStaleTime: 0,
  })
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return router
}

function createClient(onInternalServerError?: () => void) {
  const client = createAppQueryClient(onInternalServerError)
  client.setQueryData(['status'], {})
  clients.push(client)
  return client
}

function serverError(status: number) {
  const config = { headers: new AxiosHeaders() }
  return new AxiosError(
    `Request failed with status code ${status}`,
    'ERR_BAD_RESPONSE',
    config,
    undefined,
    {
      data: { success: false, message: 'Upstream unavailable' },
      status,
      statusText: 'Error',
      headers: {},
      config,
    }
  )
}

let tokenRequests: string[]

beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  localStorage.clear()
  tokenRequests = []
})

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
  clients.splice(0).forEach((client) => client.clear())
})

test('hovering the keys link prefetches the first page once, and the page renders it without another request', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url.startsWith('/api/token/')) {
      tokenRequests.push(url)
      return { data: keysPage }
    }
    return { data: { success: true, data: { default: { ratio: 1 } } } }
  })
  const router = renderApp(createClient())
  const user = userEvent.setup()
  const link = await screen.findByRole('link', { name: 'API Keys' })

  await user.hover(link)
  await waitFor(() =>
    expect(tokenRequests).toEqual(['/api/token/?p=1&size=20'])
  )
  await user.unhover(link)
  await router.preloadRoute({ to: '/user/keys' })
  await user.click(link)

  expect(await screen.findByText(key.name)).toBeInTheDocument()
  expect(router.state.location.pathname).toBe('/user/keys')
  expect(tokenRequests).toEqual(['/api/token/?p=1&size=20'])
})

test('a failed preload does not reject, toast or redirect, and the page loads its own data after navigating', async () => {
  const onInternalServerError = vi.fn()
  const notify = vi.spyOn(toast, 'error').mockReturnValue('error')
  vi.spyOn(api, 'get').mockImplementation(async (url: string) => {
    if (url.startsWith('/api/token/')) {
      tokenRequests.push(url)
      if (tokenRequests.length === 1) throw serverError(500)
      return { data: keysPage }
    }
    return { data: { success: true, data: { default: { ratio: 1 } } } }
  })
  const client = createClient(onInternalServerError)
  const router = renderApp(client)
  const user = userEvent.setup()
  const link = await screen.findByRole('link', { name: 'API Keys' })

  await expect(router.preloadRoute({ to: '/user/keys' })).resolves.toBeDefined()
  const keysQuery = () =>
    client.getQueryCache().find({ queryKey: ['keys'], exact: false })
  await waitFor(() => expect(keysQuery()?.state.status).toBe('error'))
  expect(tokenRequests).toHaveLength(1)
  expect(notify).not.toHaveBeenCalled()
  expect(onInternalServerError).not.toHaveBeenCalled()

  await user.click(link)

  expect(await screen.findByText(key.name)).toBeInTheDocument()
  expect(router.state.location.pathname).toBe('/user/keys')
  expect(tokenRequests).toHaveLength(2)
  expect(notify).not.toHaveBeenCalled()
  expect(onInternalServerError).not.toHaveBeenCalled()
})
