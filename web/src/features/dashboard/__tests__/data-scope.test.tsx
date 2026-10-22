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
import { cleanup, render, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import type { DataScope } from '@/lib/workspace'
import { useAuthStore } from '@/stores/auth-store'

import { getFlowQuotaDates, getUserQuotaDates } from '../api'
import { LogStatCards } from '../components/models/log-stat-cards'

const range = { start_timestamp: 1788600000, end_timestamp: 1788686400 }

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
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
