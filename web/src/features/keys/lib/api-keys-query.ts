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
import i18next from 'i18next'

import type {
  TableUrlState,
  TableUrlStateConfig,
} from '@/hooks/use-table-url-state'
import { createServerError } from '@/lib/server-error-message'

import { getApiKeys, searchApiKeys } from '../api'
import { ERROR_MESSAGES } from '../constants'

/** How the API keys table keeps its page and filters in the URL. */
export const API_KEYS_TABLE_URL_CONFIG: TableUrlStateConfig = {
  pagination: { defaultPage: 1, defaultPageSize: 20 },
  globalFilter: { enabled: true, key: 'filter' },
  columnFilters: [
    { columnId: 'status', searchKey: 'status', type: 'array' },
    { columnId: '_tokenSearch', searchKey: 'token', type: 'string' },
  ],
}

/**
 * The page of API keys the table shows. `refreshTrigger` is the table's
 * manual refresh counter, which starts at 0 on every visit.
 */
export function apiKeysListQueryOptions(
  table: TableUrlState,
  refreshTrigger: number
) {
  const page = table.pagination.pageIndex + 1
  const pageSize = table.pagination.pageSize
  const keyword = table.globalFilter
  const token =
    (table.columnFilters.find((filter) => filter.id === '_tokenSearch')
      ?.value as string | undefined) ?? ''
  const shouldSearch = Boolean(keyword?.trim() || token.trim())

  return queryOptions({
    queryKey: ['keys', page, pageSize, keyword, token, refreshTrigger],
    queryFn: async () => {
      const result = shouldSearch
        ? await searchApiKeys({ keyword, token, p: page, size: pageSize })
        : await getApiKeys({ p: page, size: pageSize })

      if (!result.success) {
        throw createServerError(
          result,
          i18next.t(
            shouldSearch
              ? ERROR_MESSAGES.SEARCH_FAILED
              : ERROR_MESSAGES.LOAD_FAILED
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
