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
import { isRedirect } from '@tanstack/react-router'
import { afterEach, describe, expect, it } from 'vitest'

import { ROLE } from '@/lib/roles'
import { getWorkspaceFromPathname } from '@/lib/workspace'
import { Route as AdminWorkspaceRoute } from '@/routes/_authenticated/admin/route'
import { useAuthStore } from '@/stores/auth-store'

afterEach(() => {
  useAuthStore.setState(useAuthStore.getInitialState(), true)
})

function runAdminGuard() {
  const beforeLoad = AdminWorkspaceRoute.options.beforeLoad as () => void
  try {
    beforeLoad()
  } catch (error) {
    return error
  }
  return null
}

describe('workspace of a path', () => {
  it.each([
    ['/user', 'user'],
    ['/user/wallet', 'user'],
    ['/admin/system-settings/site/system-info', 'admin'],
    ['/users', undefined],
    ['/pricing', undefined],
    ['/', undefined],
  ])('reads %s as %s', (pathname, workspace) => {
    expect(getWorkspaceFromPathname(pathname)).toBe(workspace)
  })
})

describe('admin workspace guard', () => {
  it('sends a regular user on an admin path to the user workspace', () => {
    useAuthStore.getState().auth.setUser({ id: 2, username: 'u', role: 1 })
    const result = runAdminGuard()
    expect(isRedirect(result)).toBe(true)
    expect((result as { options: { to?: string } } | null)?.options.to).toBe(
      '/user'
    )
  })

  it.each([ROLE.ADMIN, ROLE.SUPER_ADMIN])(
    'lets role %i into the admin workspace',
    (role) => {
      useAuthStore.getState().auth.setUser({ id: 1, username: 'a', role })
      expect(runAdminGuard()).toBe(null)
    }
  )
})
