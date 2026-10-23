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
import z from 'zod'

import { PaymentOrders } from '@/features/payment-orders'
import {
  PAYMENT_ORDERS_DEFAULT_SECTION,
  PAYMENT_ORDERS_SECTION_IDS,
} from '@/features/payment-orders/constants'
import {
  PAYMENT_ORDERS_TABLE_URL_CONFIG,
  PAYMENT_UNMATCHED_TABLE_URL_CONFIG,
  paymentOrdersListQueryOptions,
  paymentUnmatchedListQueryOptions,
} from '@/features/payment-orders/lib/payment-orders-query'
import { readTableUrlState } from '@/hooks/use-table-url-state'
import { prefetchRouteData } from '@/lib/route-prefetch'

const paymentOrdersSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(20),
  filter: z.string().optional().catch(''),
  user: z.string().optional().catch(''),
  status: z.array(z.string()).optional().catch([]),
  purpose: z.array(z.string()).optional().catch([]),
  platform: z.array(z.string()).optional().catch([]),
  uPage: z.number().optional().catch(1),
  uPageSize: z.number().optional().catch(20),
  uState: z.array(z.string()).optional().catch([]),
})

export const Route = createFileRoute(
  '/_authenticated/admin/payment-orders/$section'
)({
  beforeLoad: ({ params }) => {
    const sections: readonly string[] = PAYMENT_ORDERS_SECTION_IDS
    if (!sections.includes(params.section)) {
      throw redirect({
        to: '/admin/payment-orders/$section',
        params: { section: PAYMENT_ORDERS_DEFAULT_SECTION },
      })
    }
  },
  validateSearch: paymentOrdersSearchSchema,
  loaderDeps: ({ search }) => search,
  loader: ({ context, params, deps }) => {
    if (params.section === 'unmatched') {
      const table = readTableUrlState(deps, PAYMENT_UNMATCHED_TABLE_URL_CONFIG)
      prefetchRouteData(
        context.queryClient,
        paymentUnmatchedListQueryOptions(table)
      )
      return
    }
    const table = readTableUrlState(deps, PAYMENT_ORDERS_TABLE_URL_CONFIG)
    prefetchRouteData(context.queryClient, paymentOrdersListQueryOptions(table))
  },
  component: PaymentOrders,
})
