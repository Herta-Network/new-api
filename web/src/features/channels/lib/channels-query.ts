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

import type {
  TableUrlState,
  TableUrlStateConfig,
} from '@/hooks/use-table-url-state'
import { requireServerSuccess } from '@/lib/server-error-message'

import { getChannelOps, getChannels, searchChannels } from '../api'
import type { ChannelSortBy } from '../types'

// The channels page's queries and the stored view choices they depend on.
// Kept apart from the components so the route loader can prefetch them.

export const channelsQueryKeys = {
  all: ['channels'] as const,
  defaultBaseURLs: () =>
    [...channelsQueryKeys.all, 'default_base_urls'] as const,
  lists: () => [...channelsQueryKeys.all, 'list'] as const,
  list: (params: Record<string, unknown>) =>
    [...channelsQueryKeys.lists(), params] as const,
  details: () => [...channelsQueryKeys.all, 'detail'] as const,
  detail: (id: number) => [...channelsQueryKeys.details(), id] as const,
}

export const CHANNELS_STATUS_FILTER_STORAGE_KEY = 'channel-status-filter'
export const CHANNELS_TAG_MODE_STORAGE_KEY = 'enable-tag-mode'
export const CHANNELS_ID_SORT_STORAGE_KEY = 'channels-id-sort'

// Kept out of `../constants`, which the route loader would otherwise pull
// into the entry bundle.
const DEFAULT_PAGE_SIZE = 20

const CHANNEL_SORTABLE_COLUMNS = new Set<ChannelSortBy>([
  'id',
  'name',
  'priority',
  'balance',
  'response_time',
  'test_time',
])

function readStoredValue(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

/** Whether the table groups channels by tag, as the admin last chose. */
export function readStoredTagMode(): boolean {
  return readStoredValue(CHANNELS_TAG_MODE_STORAGE_KEY) === 'true'
}

/** The sorting a visit starts with: by ID or by priority, as last chosen. */
export function readStoredSorting(): SortingState {
  const idSort = readStoredValue(CHANNELS_ID_SORT_STORAGE_KEY) === 'true'
  return [{ id: idSort ? 'id' : 'priority', desc: true }]
}

/**
 * How the channels table keeps its page and filters in the URL. Without a
 * status in the URL, the status filter the admin last chose applies.
 */
export function getChannelsTableUrlConfig(
  isMobile: boolean
): TableUrlStateConfig {
  return {
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      {
        columnId: 'status',
        searchKey: 'status',
        type: 'array',
        deserialize: (value) => {
          if (value !== undefined) return value
          const stored = readStoredValue(CHANNELS_STATUS_FILTER_STORAGE_KEY)
          return stored === 'enabled' || stored === 'disabled' ? [stored] : []
        },
      },
      { columnId: 'type', searchKey: 'type', type: 'array' },
      { columnId: 'group', searchKey: 'group', type: 'array' },
      { columnId: 'model', searchKey: 'model', type: 'string' },
    ],
  }
}

/** Retry and auto-ban settings shown beside the page title. */
export const channelOpsQueryOptions = queryOptions({
  queryKey: ['channel-ops'],
  queryFn: async () => requireServerSuccess(await getChannelOps()),
  retry: false,
  staleTime: 5 * 60 * 1000,
})

/** The first value of an array filter, unless it is the "all" choice. */
function getSelectedFilter(
  table: TableUrlState,
  columnId: string
): string | undefined {
  const values =
    (table.columnFilters.find((filter) => filter.id === columnId)?.value as
      | string[]
      | undefined) ?? []
  return values.length > 0 && !values.includes('all') ? values[0] : undefined
}

/** The page of channels the table shows for its filters and view choices. */
export function channelsListQueryOptions(
  table: TableUrlState,
  view: { enableTagMode: boolean; sorting: SortingState }
) {
  const keyword = table.globalFilter
  const model =
    (table.columnFilters.find((filter) => filter.id === 'model')?.value as
      | string
      | undefined) ?? ''
  const type = getSelectedFilter(table, 'type')
  const activeSort = view.sorting[0]
  const sortParams =
    activeSort && CHANNEL_SORTABLE_COLUMNS.has(activeSort.id as ChannelSortBy)
      ? ({
          sort_by: activeSort.id as ChannelSortBy,
          sort_order: activeSort.desc ? 'desc' : 'asc',
        } as const)
      : {}
  const listParams = {
    group: getSelectedFilter(table, 'group'),
    status: getSelectedFilter(table, 'status'),
    type: type === undefined ? undefined : Number(type),
    tag_mode: view.enableTagMode,
    id_sort: activeSort?.id === 'id',
    ...sortParams,
    p: table.pagination.pageIndex + 1,
    page_size: table.pagination.pageSize,
  }
  const shouldSearch = Boolean(keyword?.trim() || model.trim())

  return queryOptions({
    queryKey: channelsQueryKeys.list({ keyword, model, ...listParams }),
    queryFn: async () =>
      requireServerSuccess(
        shouldSearch
          ? await searchChannels({ keyword, model, ...listParams })
          : await getChannels(listParams)
      ),
  })
}
