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
import { queryOptions } from '@tanstack/react-query'

import { getGroups } from '@/features/users/api'
import { getUserGroups } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

/**
 * Group lists change only when an administrator edits the group settings,
 * and saving those settings invalidates both lists (`useUpdateOption`).
 */
export const GROUP_LIST_STALE_TIME = 5 * 60 * 1000

/** Every group the site defines (`/api/group/`), for admin filters and forms. */
export const siteGroupsQueryOptions = queryOptions({
  queryKey: ['groups'],
  queryFn: async () => requireServerSuccess(await getGroups()),
  staleTime: GROUP_LIST_STALE_TIME,
})

/** The groups the signed-in user may use, with their ratios. */
export const userGroupsQueryOptions = queryOptions({
  queryKey: ['user-groups'],
  queryFn: async () => requireServerSuccess(await getUserGroups()),
  staleTime: GROUP_LIST_STALE_TIME,
})
