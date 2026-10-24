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
  createRouter,
  RouterContextProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import type { ModelCatalogStatus } from '@/features/models/types'
import { api } from '@/lib/api'

import { ModelCatalogNotice } from '../model-catalog-notice'

const visible: ModelCatalogStatus = {
  registered: true,
  listed: true,
  priced: true,
}

function mockCatalog(statuses: Record<string, ModelCatalogStatus>) {
  return vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: statuses } })
}

function renderNotice(models: string[]) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute(),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  return render(
    <QueryClientProvider client={client}>
      <RouterContextProvider router={router}>
        <ModelCatalogNotice models={models} />
      </RouterContextProvider>
    </QueryClientProvider>
  )
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ModelCatalogNotice', () => {
  test('lists unpriced and hidden models when users cannot see them', async () => {
    const post = mockCatalog({
      'new-model': { registered: false, listed: true, priced: false },
      'retired-model': { registered: true, listed: false, priced: true },
      'sold-model': visible,
    })

    renderNotice(['new-model', 'retired-model', 'sold-model'])

    const notice = await screen.findByRole('status')
    expect(notice).toHaveTextContent('Unpriced: new-model')
    expect(notice).toHaveTextContent('Hidden by display policy: retired-model')
    expect(notice).not.toHaveTextContent('sold-model')
    const link = screen.getByRole('link', { name: 'Open model management' })
    expect(link).toHaveAttribute('href', '/admin/models/metadata')
    expect(link).toHaveAttribute('target', '_blank')
    expect(post).toHaveBeenCalledWith('/api/models/catalog_status', {
      models: ['new-model', 'retired-model', 'sold-model'],
    })
  })

  test('shows the first five names and counts the rest when many models are unpriced', async () => {
    const names = Array.from({ length: 7 }, (_, index) => `model-${index + 1}`)
    mockCatalog(
      Object.fromEntries(
        names.map((name) => [name, { ...visible, priced: false }])
      )
    )

    renderNotice(names)

    expect(await screen.findByRole('status')).toHaveTextContent(
      'Unpriced: model-1, model-2, model-3, model-4, model-5 +2 more'
    )
  })

  test('renders nothing when every supplied model is priced and listed', async () => {
    const post = mockCatalog({ 'sold-model': visible })

    renderNotice(['sold-model'])

    await waitFor(() => expect(post).toHaveBeenCalled())
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
  })

  test('skips the lookup when the channel has no models', async () => {
    const post = mockCatalog({})

    renderNotice([])

    await new Promise((resolve) => setTimeout(resolve, 500))
    expect(post).not.toHaveBeenCalled()
    expect(screen.queryByRole('status')).toBeNull()
  })
})
