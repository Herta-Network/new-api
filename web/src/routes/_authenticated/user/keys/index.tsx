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
import { createFileRoute } from '@tanstack/react-router'
import z from 'zod'

import { ApiKeys } from '@/features/keys'
import { API_KEY_STATUS_OPTIONS } from '@/features/keys/constants'
import {
  API_KEYS_TABLE_URL_CONFIG,
  apiKeysListQueryOptions,
} from '@/features/keys/lib/api-keys-query'
import { readTableUrlState } from '@/hooks/use-table-url-state'
import { userGroupsQueryOptions } from '@/lib/group-queries'
import { prefetchRouteData } from '@/lib/route-prefetch'

const apiKeySearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(undefined),
  status: z
    .array(z.enum(API_KEY_STATUS_OPTIONS.map((s) => s.value as `${number}`)))
    .optional()
    .catch([]),
  filter: z.string().optional().catch(''),
  token: z.string().optional().catch(''),
})

export const Route = createFileRoute('/_authenticated/user/keys/')({
  validateSearch: apiKeySearchSchema,
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => {
    // A visit starts with the table's refresh counter at 0.
    const table = readTableUrlState(deps, API_KEYS_TABLE_URL_CONFIG)
    prefetchRouteData(context.queryClient, apiKeysListQueryOptions(table, 0))
    prefetchRouteData(context.queryClient, userGroupsQueryOptions)
  },
  component: ApiKeys,
})
