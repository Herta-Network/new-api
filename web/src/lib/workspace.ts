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
import { getCookie, setCookie } from '@/lib/cookies'
import { ROLE } from '@/lib/roles'

/**
 * The console has one workspace per audience: `/user` for everyone's own
 * account, keys and usage, `/admin` for operating the site. The first path
 * segment decides the workspace.
 */
export const WORKSPACES = ['user', 'admin'] as const

export type Workspace = (typeof WORKSPACES)[number]

/**
 * Whose data a page shows. A page that exists in both workspaces gets its
 * scope from its route, never from the viewer's role, so an administrator in
 * the user workspace sees only their own records.
 */
export type DataScope = 'self' | 'site'

export const WORKSPACE_HOME = {
  user: '/user',
  admin: '/admin',
} as const satisfies Record<Workspace, string>

const WORKSPACE_COOKIE_NAME = 'console_workspace'
const WORKSPACE_COOKIE_MAX_AGE = 60 * 60 * 24 * 365 // 1 year

export function getWorkspaceFromPathname(
  pathname: string
): Workspace | undefined {
  const segment = pathname.split('/')[1]
  return WORKSPACES.find((workspace) => workspace === segment)
}

export function canUseAdminWorkspace(role: number | undefined): boolean {
  return (role ?? ROLE.GUEST) >= ROLE.ADMIN
}

export function rememberWorkspace(workspace: Workspace): void {
  if (getCookie(WORKSPACE_COOKIE_NAME) === workspace) return
  setCookie(WORKSPACE_COOKIE_NAME, workspace, WORKSPACE_COOKIE_MAX_AGE)
}

/**
 * Where a signed-in user lands when nothing asked for a specific page:
 * administrators return to the workspace they used last, everyone else goes
 * to the user workspace.
 */
export function getWorkspaceLanding(
  role: number | undefined
): (typeof WORKSPACE_HOME)[Workspace] {
  if (
    canUseAdminWorkspace(role) &&
    getCookie(WORKSPACE_COOKIE_NAME) === 'admin'
  ) {
    return WORKSPACE_HOME.admin
  }
  return WORKSPACE_HOME.user
}
