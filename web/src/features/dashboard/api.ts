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
import { api } from '@/lib/api'
import type { DataScope } from '@/lib/workspace'

import type {
  ConsoleOverview,
  ConsoleRankingPeriod,
  ConsoleRankings,
  ConsoleRevenue,
  ConsoleUsers,
  FlowQuotaDataItem,
  QuotaDataItem,
  UptimeGroupResult,
} from './types'

// ============================================================================
// Dashboard APIs
// ============================================================================

// ----------------------------------------------------------------------------
// Quota & Usage Data
// ----------------------------------------------------------------------------

// Get quota data within a time range: the signed-in user's own (`self`) or
// every user's (`site`, admin only).
export async function getUserQuotaDates(
  params: {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  scope: DataScope = 'self'
) {
  const endpoint = scope === 'site' ? '/api/data' : '/api/data/self'
  const res = await api.get<{ success: boolean; data: QuotaDataItem[] }>(
    endpoint,
    { params }
  )
  return res.data
}

// ----------------------------------------------------------------------------
// System Monitoring
// ----------------------------------------------------------------------------

export async function getUserQuotaDataByUsers(params: {
  start_timestamp: number
  end_timestamp: number
}) {
  const res = await api.get<{ success: boolean; data: QuotaDataItem[] }>(
    '/api/data/users',
    { params }
  )
  return res.data
}

export async function getFlowQuotaDates(
  params: {
    start_timestamp: number
    end_timestamp: number
    default_time?: string
    username?: string
  },
  scope: DataScope = 'self'
) {
  const endpoint = scope === 'site' ? '/api/data/flow' : '/api/data/flow/self'
  const res = await api.get<{
    success: boolean
    data?: FlowQuotaDataItem[]
    message?: string
  }>(endpoint, { params })
  return res.data
}

// Get uptime monitoring status for all services
export async function getUptimeStatus() {
  const res = await api.get<{ success: boolean; data: UptimeGroupResult[] }>(
    '/api/uptime/status'
  )
  return res.data
}

// ----------------------------------------------------------------------------
// Admin Operations Overview
// ----------------------------------------------------------------------------

type ConsoleResponse<T> = { success: boolean; message?: string; data: T }

export async function getConsoleOverview() {
  const res = await api.get<ConsoleResponse<ConsoleOverview>>(
    '/api/admin/console/overview'
  )
  return res.data
}

export async function getConsoleRevenue() {
  const res = await api.get<ConsoleResponse<ConsoleRevenue>>(
    '/api/admin/console/revenue'
  )
  return res.data
}

export async function getConsoleUsers() {
  const res = await api.get<ConsoleResponse<ConsoleUsers>>(
    '/api/admin/console/users'
  )
  return res.data
}

export async function getConsoleRankings(period: ConsoleRankingPeriod) {
  const res = await api.get<ConsoleResponse<ConsoleRankings>>(
    '/api/admin/console/rankings',
    { params: { period } }
  )
  return res.data
}
