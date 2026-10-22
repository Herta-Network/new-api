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
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, afterEach, beforeAll, expect, it } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'
import { LayoutProvider } from '@/context/layout-provider'
import { ROLE } from '@/lib/roles'
import { STATUS_QUERY_KEY } from '@/lib/status-query'
import { useAuthStore } from '@/stores/auth-store'

import { AppSidebar } from '../app-sidebar'

// jsdom lacks the Web Animations API the menu's scroll area waits on.
const getAnimationsDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)

beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})

afterAll(() => {
  if (getAnimationsDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      getAnimationsDescriptor
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})

afterEach(() => {
  cleanup()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

async function renderSidebar(role: number, path: string) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(STATUS_QUERY_KEY, { SidebarModulesAdmin: '' })
  const root = createRootRoute({
    component: () => (
      <LayoutProvider>
        <SidebarProvider>
          <AppSidebar />
          <Outlet />
        </SidebarProvider>
      </LayoutProvider>
    ),
  })
  const pages = [
    '/user',
    '/user/dashboard/$section',
    '/admin',
    '/admin/channels',
  ]
  const router = createRouter({
    routeTree: root.addChildren(
      pages.map((page) =>
        createRoute({ getParentRoute: () => root, path: page })
      )
    ),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

it('hides the workspace switcher from regular users', async () => {
  await renderSidebar(ROLE.USER, '/user/dashboard/overview')

  expect(await screen.findByRole('link', { name: 'Wallet' })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: /User Console/ })
  ).not.toBeInTheDocument()
})

it('shows admins the active workspace and opens the admin home from the user workspace', async () => {
  const user = userEvent.setup()
  const router = await renderSidebar(ROLE.ADMIN, '/user/dashboard/overview')

  await user.click(await screen.findByRole('button', { name: /User Console/ }))
  expect(
    await screen.findByRole('menuitemradio', { name: 'User Console' })
  ).toHaveAttribute('aria-checked', 'true')
  await user.click(screen.getByRole('menuitemradio', { name: 'Admin Console' }))

  await waitFor(() => expect(router.state.location.pathname).toBe('/admin'))
  await waitFor(() =>
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
  )
  expect(
    await screen.findByRole('button', { name: /Admin Console/ })
  ).toBeVisible()
  expect(await screen.findByRole('link', { name: 'Channels' })).toBeVisible()
  expect(screen.queryByRole('link', { name: 'Wallet' })).not.toBeInTheDocument()
})

it('returns admins to the user home from the admin workspace', async () => {
  const user = userEvent.setup()
  const router = await renderSidebar(ROLE.SUPER_ADMIN, '/admin/channels')

  await user.click(await screen.findByRole('button', { name: /Admin Console/ }))
  await user.click(
    await screen.findByRole('menuitemradio', { name: 'User Console' })
  )

  await waitFor(() => expect(router.state.location.pathname).toBe('/user'))
  expect(await screen.findByRole('link', { name: 'Wallet' })).toBeVisible()
})
