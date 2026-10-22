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
import type { DataScope } from '@/lib/workspace'

/**
 * Dashboard sections. The user workspace shows the signed-in user's own
 * data; the admin workspace shows the whole site and adds the sections that
 * only make sense site-wide.
 */
export const DASHBOARD_SECTION_TITLE_KEYS = {
  overview: 'Overview',
  models: 'Model Call Analytics',
  flow: 'Flow',
  users: 'User Analytics',
  performance: 'Performance',
} as const

export type DashboardSectionId = keyof typeof DASHBOARD_SECTION_TITLE_KEYS

export const DASHBOARD_SECTIONS: Record<
  DataScope,
  readonly DashboardSectionId[]
> = {
  self: ['overview', 'models', 'flow'],
  site: ['models', 'flow', 'users', 'performance'],
}

export const DASHBOARD_DEFAULT_SECTION: Record<DataScope, DashboardSectionId> =
  {
    self: 'overview',
    site: 'models',
  }

export function isDashboardSection(
  scope: DataScope,
  section: string
): section is DashboardSectionId {
  return (DASHBOARD_SECTIONS[scope] as readonly string[]).includes(section)
}
