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
import type { SortingState } from '@tanstack/react-table'
import i18next from 'i18next'

import type {
  TableUrlState,
  TableUrlStateConfig,
} from '@/hooks/use-table-url-state'
import { createServerError } from '@/lib/server-error-message'

import { getUsers, searchUsers } from '../api'
import type { UserSortBy } from '../types'

const USER_SORTABLE_COLUMNS = new Set<UserSortBy>([
  'id',
  'username',
  'quota',
  'group',
  'created_at',
  'last_login_at',
])

/** How the users table keeps its page and filters in the URL. */
export function getUsersTableUrlConfig(isMobile: boolean): TableUrlStateConfig {
  return {
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'role', searchKey: 'role', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'array' },
    ],
  }
}

function getArrayFilter(table: TableUrlState, columnId: string): string[] {
  return (
    (table.columnFilters.find((filter) => filter.id === columnId)?.value as
      | string[]
      | undefined) ?? []
  )
}

/**
 * The page of users the table shows. `refreshTrigger` is the table's manual
 * refresh counter, which starts at 0 on every visit.
 */
export function usersListQueryOptions(
  table: TableUrlState,
  sorting: SortingState,
  refreshTrigger: number
) {
  const page = table.pagination.pageIndex + 1
  const pageSize = table.pagination.pageSize
  const globalFilter = table.globalFilter
  const statusFilter = getArrayFilter(table, 'status')
  const roleFilter = getArrayFilter(table, 'role')
  const groupFilter = getArrayFilter(table, 'group')
  const activeSort = sorting[0]
  const sortParams =
    activeSort && USER_SORTABLE_COLUMNS.has(activeSort.id as UserSortBy)
      ? ({
          sort_by: activeSort.id as UserSortBy,
          sort_order: activeSort.desc ? 'desc' : 'asc',
        } as const)
      : {}

  return queryOptions({
    queryKey: [
      'users',
      page,
      pageSize,
      globalFilter,
      statusFilter,
      roleFilter,
      groupFilter,
      sortParams,
      refreshTrigger,
    ],
    queryFn: async () => {
      const hasFilter = globalFilter?.trim()
      const hasColumnFilter =
        statusFilter.length > 0 ||
        roleFilter.length > 0 ||
        groupFilter.length > 0
      const params = { p: page, page_size: pageSize, ...sortParams }

      const result =
        hasFilter || hasColumnFilter
          ? await searchUsers({
              ...params,
              keyword: globalFilter,
              status: statusFilter[0] ?? '',
              role: roleFilter[0] ?? '',
              group: groupFilter[0] ?? '',
            })
          : await getUsers(params)

      if (!result.success) {
        throw createServerError(
          result,
          i18next.t(
            hasFilter ? 'Failed to search users' : 'Failed to load users'
          )
        )
      }

      return {
        items: result.data?.items || [],
        total: result.data?.total || 0,
      }
    },
  })
}
