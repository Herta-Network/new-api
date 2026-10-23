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
import { createFileRoute, redirect } from '@tanstack/react-router'

import { Dashboard } from '@/features/dashboard'
import {
  overviewApiKeysQueryOptions,
  overviewUsageTrendQueryOptions,
  overviewUserModelsQueryOptions,
} from '@/features/dashboard/lib/overview-queries'
import {
  DASHBOARD_DEFAULT_SECTION,
  isDashboardSection,
} from '@/features/dashboard/section-registry'
import { prefetchRouteData } from '@/lib/route-prefetch'

export const Route = createFileRoute('/_authenticated/user/dashboard/$section')(
  {
    beforeLoad: ({ params }) => {
      if (!isDashboardSection('self', params.section)) {
        throw redirect({
          to: '/user/dashboard/$section',
          params: { section: DASHBOARD_DEFAULT_SECTION.self },
        })
      }
    },
    loader: ({ context, params }) => {
      if (params.section !== 'overview') return
      prefetchRouteData(context.queryClient, overviewApiKeysQueryOptions)
      prefetchRouteData(context.queryClient, overviewUserModelsQueryOptions)
      prefetchRouteData(context.queryClient, overviewUsageTrendQueryOptions)
    },
    component: UserDashboardPage,
  }
)

function UserDashboardPage() {
  const { section } = Route.useParams()
  return (
    <Dashboard
      scope='self'
      section={
        isDashboardSection('self', section)
          ? section
          : DASHBOARD_DEFAULT_SECTION.self
      }
    />
  )
}
