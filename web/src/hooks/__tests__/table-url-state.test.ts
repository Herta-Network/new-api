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
import { renderHook } from '@testing-library/react'
import { afterEach, describe, expect, test } from 'vitest'

import { getChannelsTableUrlConfig } from '@/features/channels/lib/channels-query'
import {
  readTableUrlState,
  useTableUrlState,
  type TableUrlStateConfig,
} from '@/hooks/use-table-url-state'

/**
 * Route loaders prefetch the page of data a table requests first, so the
 * state they read from the URL must be the state the table starts from.
 */
function initialHookState(
  search: Record<string, unknown>,
  config: TableUrlStateConfig
) {
  const { result } = renderHook(() =>
    useTableUrlState({ search, navigate: () => undefined, ...config })
  )
  return {
    globalFilter: result.current.globalFilter,
    columnFilters: result.current.columnFilters,
    pagination: result.current.pagination,
  }
}

afterEach(() => {
  localStorage.clear()
})

describe('readTableUrlState', () => {
  test.each([
    ['no search params', {}],
    [
      'a page, a page size, a filter and column filters',
      { page: 3, pageSize: 50, filter: 'gpt', type: ['1'], model: 'claude' },
    ],
  ])('with %s matches the state the table starts from', (_label, search) => {
    const config = getChannelsTableUrlConfig(false)

    expect(readTableUrlState(search, config)).toEqual(
      initialHookState(search, config)
    )
  })

  test('without a page size in the URL uses the stored page size like the table', () => {
    localStorage.setItem('page-size', '50')
    const config = getChannelsTableUrlConfig(false)

    const state = readTableUrlState({}, config)

    expect(state.pagination).toEqual({ pageIndex: 0, pageSize: 50 })
    expect(state).toEqual(initialHookState({}, config))
  })

  test('without a status in the URL applies the stored status filter like the table', () => {
    localStorage.setItem('channel-status-filter', 'enabled')
    const config = getChannelsTableUrlConfig(false)

    const state = readTableUrlState({}, config)

    expect(state.columnFilters).toEqual([{ id: 'status', value: ['enabled'] }])
    expect(state).toEqual(initialHookState({}, config))
  })

  test('with the global filter disabled reads no global filter', () => {
    const config: TableUrlStateConfig = { globalFilter: { enabled: false } }

    expect(readTableUrlState({ filter: 'ignored' }, config).globalFilter).toBe(
      undefined
    )
  })
})
