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
/* eslint-disable react-refresh/only-export-components */
import { getRouteApi } from '@tanstack/react-router'
import { createContext, useContext, useState, type ReactNode } from 'react'

import { ROLE } from '@/lib/roles'
import type { DataScope } from '@/lib/workspace'
import { useAuthStore } from '@/stores/auth-store'

import { resolveLogsViewAccess } from '../lib/usage-logs-query'
import type { ChannelAffinityInfo } from '../types'

const userUsageLogsRoute = getRouteApi(
  '/_authenticated/user/usage-logs/$section'
)

// Both workspaces validate the same search schema, so the admin route is
// read through the user route's types.
const USAGE_LOGS_ROUTES: Record<DataScope, typeof userUsageLogsRoute> = {
  self: userUsageLogsRoute,
  site: getRouteApi(
    '/_authenticated/admin/usage-logs/$section'
  ) as unknown as typeof userUsageLogsRoute,
}

interface UsageLogsContextValue {
  selectedUserId: number | null
  setSelectedUserId: (userId: number | null) => void
  userInfoDialogOpen: boolean
  setUserInfoDialogOpen: (open: boolean) => void
  affinityTarget: ChannelAffinityInfo | null
  setAffinityTarget: (target: ChannelAffinityInfo | null) => void
  affinityDialogOpen: boolean
  setAffinityDialogOpen: (open: boolean) => void
  sensitiveVisible: boolean
  setSensitiveVisible: (visible: boolean) => void
  scope: DataScope
}

const UsageLogsContext = createContext<UsageLogsContextValue | undefined>(
  undefined
)

export function UsageLogsProvider(props: {
  scope: DataScope
  children: ReactNode
}) {
  const [selectedUserId, setSelectedUserId] = useState<number | null>(null)
  const [userInfoDialogOpen, setUserInfoDialogOpen] = useState(false)
  const [affinityTarget, setAffinityTarget] =
    useState<ChannelAffinityInfo | null>(null)
  const [affinityDialogOpen, setAffinityDialogOpen] = useState(false)
  const [sensitiveVisible, setSensitiveVisible] = useState(true)

  return (
    <UsageLogsContext.Provider
      value={{
        selectedUserId,
        setSelectedUserId,
        userInfoDialogOpen,
        setUserInfoDialogOpen,
        affinityTarget,
        setAffinityTarget,
        affinityDialogOpen,
        setAffinityDialogOpen,
        sensitiveVisible,
        setSensitiveVisible,
        scope: props.scope,
      }}
    >
      {props.children}
    </UsageLogsContext.Provider>
  )
}

export function useUsageLogsContext() {
  const context = useContext(UsageLogsContext)
  if (!context) {
    throw new Error('useUsageLogsContext must be used within UsageLogsProvider')
  }
  return context
}

/**
 * Resolves the effective scope of the usage logs page. The route decides the
 * scope (`self` in the user workspace, `site` in the admin workspace); the
 * role only narrows it, so data fetching and admin-only UI key off
 * `isAdminView`, and an admin in the user workspace is treated exactly like
 * a regular user.
 */
export function useLogsViewScope() {
  const role = useAuthStore((state) => state.auth.user?.role ?? ROLE.GUEST)
  const { scope } = useUsageLogsContext()
  const viewAccess = resolveLogsViewAccess(role, scope)

  return {
    isAdminView: viewAccess !== 'self',
    isRootView: viewAccess === 'root',
    viewAccess,
  }
}

/** The usage logs route of the page's workspace, for its params and search. */
export function useUsageLogsRoute() {
  return USAGE_LOGS_ROUTES[useUsageLogsContext().scope]
}
