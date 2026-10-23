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
import { queryOptions, type QueryClient } from '@tanstack/react-query'
import i18next from 'i18next'

import {
  getAdminPlans,
  getSelfSubscriptionFull,
} from '@/features/subscriptions/api'
import { MOBILE_MEDIA_QUERY, matchesMediaQuery } from '@/hooks/use-media-query'
import {
  readTableUrlState,
  type TableUrlState,
  type TableUrlStateConfig,
} from '@/hooks/use-table-url-state'
import {
  siteGroupsQueryOptions,
  userGroupsQueryOptions,
} from '@/lib/group-queries'
import { ROLE } from '@/lib/roles'
import { prefetchRouteData } from '@/lib/route-prefetch'
import {
  createServerError,
  requireServerSuccess,
} from '@/lib/server-error-message'
import type { DataScope } from '@/lib/workspace'
import { useAuthStore } from '@/stores/auth-store'

import { getLogStats, getUserLogStats } from '../api'
import {
  DEFAULT_LOG_STATS,
  DEFAULT_LOGS_DATA,
  LOG_TYPE_ALL_VALUE,
} from '../constants'
import { isUsageLogsSectionId } from '../section-registry'
import type { LogCategory } from '../types'
import { shouldShowBillingSource } from './billing-source'
import { buildApiParams, fetchLogsByCategory } from './utils'

// The usage logs page's queries, shared by its route loaders and components.

export type LogsViewAccess = 'self' | 'admin' | 'root'

export function resolveLogsViewAccess(
  role: number,
  scope: DataScope
): LogsViewAccess {
  if (scope !== 'site' || role < ROLE.ADMIN) return 'self'
  return role === ROLE.SUPER_ADMIN ? 'root' : 'admin'
}

function deserializeLogTypeFilter(value: unknown): unknown[] {
  let values: unknown[] = []
  if (Array.isArray(value)) {
    values = value
  } else if (value) {
    values = [value]
  }
  return values.filter((item) => String(item) !== LOG_TYPE_ALL_VALUE)
}

/**
 * How the logs table keeps its page and filters in the URL. Only the admin
 * view filters by channel and user.
 */
export function getUsageLogsTableUrlConfig(
  isMobile: boolean,
  isAdmin: boolean
): TableUrlStateConfig {
  return {
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 20 : 100 },
    globalFilter: { enabled: false },
    columnFilters: [
      {
        columnId: 'created_at',
        searchKey: 'type',
        type: 'array' as const,
        deserialize: deserializeLogTypeFilter,
      },
      { columnId: 'model_name', searchKey: 'model', type: 'string' as const },
      { columnId: 'token_name', searchKey: 'token', type: 'string' as const },
      { columnId: 'group', searchKey: 'group', type: 'string' as const },
      ...(isAdmin
        ? [
            {
              columnId: 'channel',
              searchKey: 'channel',
              type: 'string' as const,
            },
            {
              columnId: 'username',
              searchKey: 'username',
              type: 'string' as const,
            },
          ]
        : []),
    ],
  }
}

/**
 * The page of logs the table shows. The key starts with the category and the
 * view access, which the table's placeholder data compares.
 */
export function usageLogsQueryOptions(args: {
  logCategory: LogCategory
  viewAccess: LogsViewAccess
  table: Pick<TableUrlState, 'columnFilters' | 'pagination'>
  searchParams: Record<string, unknown>
}) {
  const page = args.table.pagination.pageIndex + 1
  const pageSize = args.table.pagination.pageSize
  return queryOptions({
    queryKey: [
      'logs',
      args.logCategory,
      args.viewAccess,
      page,
      pageSize,
      args.table.columnFilters,
      args.searchParams,
    ],
    queryFn: async () => {
      const result = await fetchLogsByCategory({
        logCategory: args.logCategory,
        isAdmin: args.viewAccess !== 'self',
        page,
        pageSize,
        searchParams: args.searchParams,
        columnFilters: args.table.columnFilters,
      })

      if (!result?.success) {
        throw createServerError(result, i18next.t('Failed to load logs'))
      }

      return result.data || DEFAULT_LOGS_DATA
    },
  })
}

/** Usage, RPM and TPM totals for the common logs filters. */
export function usageLogStatsQueryOptions(
  isAdmin: boolean,
  searchParams: Record<string, unknown>
) {
  return queryOptions({
    queryKey: ['usage-logs-stats', isAdmin, searchParams],
    queryFn: async () => {
      const params = buildApiParams({
        page: 1,
        pageSize: 1,
        searchParams,
        columnFilters: [],
        isAdmin,
      })

      const result = isAdmin
        ? requireServerSuccess(await getLogStats(params))
        : requireServerSuccess(await getUserLogStats(params))

      return result.success
        ? result.data || DEFAULT_LOG_STATS
        : DEFAULT_LOG_STATS
    },
  })
}

/** Whether the common logs show which balance paid for each request. */
export function usageLogBillingSourceQueryOptions(
  isAdmin: boolean,
  userId: number | undefined
) {
  return queryOptions({
    queryKey: ['usage-log-billing-source', isAdmin, userId],
    queryFn: async () => {
      if (isAdmin) {
        const plansResult = await getAdminPlans()
        return shouldShowBillingSource({
          isAdmin,
          plans: plansResult.success ? plansResult.data : undefined,
          subscriptions: undefined,
        })
      }

      const selfResult = await getSelfSubscriptionFull()
      return shouldShowBillingSource({
        isAdmin,
        plans: undefined,
        subscriptions: selfResult.success
          ? selfResult.data?.subscriptions
          : undefined,
      })
    },
  })
}

/**
 * Prefetch what a usage logs section requests on mount, for the route
 * loaders of both workspaces.
 */
export function prefetchUsageLogs(
  queryClient: QueryClient,
  scope: DataScope,
  section: string,
  search: Record<string, unknown>
): void {
  if (!isUsageLogsSectionId(section)) return
  const user = useAuthStore.getState().auth.user
  const viewAccess = resolveLogsViewAccess(user?.role ?? ROLE.GUEST, scope)
  const isAdmin = viewAccess !== 'self'
  const table = readTableUrlState(
    search,
    getUsageLogsTableUrlConfig(matchesMediaQuery(MOBILE_MEDIA_QUERY), isAdmin)
  )
  prefetchRouteData(
    queryClient,
    usageLogsQueryOptions({
      logCategory: section,
      viewAccess,
      table,
      searchParams: search,
    })
  )
  if (section !== 'common') return

  prefetchRouteData(queryClient, usageLogStatsQueryOptions(isAdmin, search))
  if (isAdmin) {
    prefetchRouteData(queryClient, siteGroupsQueryOptions)
  } else {
    prefetchRouteData(queryClient, userGroupsQueryOptions)
  }
  if (user?.id != null) {
    prefetchRouteData(
      queryClient,
      usageLogBillingSourceQueryOptions(isAdmin, user.id)
    )
  }
}
