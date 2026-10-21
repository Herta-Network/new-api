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
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { PaymentOrdersTable } from './components/orders-table'
import { PaymentUnmatchedTable } from './components/unmatched-table'
import {
  PAYMENT_ORDERS_SECTION_IDS,
  PAYMENT_ORDERS_SECTION_LABELS,
  type PaymentOrdersSectionId,
} from './constants'

const route = getRouteApi('/_authenticated/payment-orders/$section')

export function PaymentOrders() {
  const { t } = useTranslation()
  const navigate = useNavigate({ from: '/payment-orders/$section' })
  const section = route.useParams().section as PaymentOrdersSectionId

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Payment Orders')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-4'>
          <Tabs
            value={section}
            onValueChange={(value) =>
              void navigate({
                to: '/payment-orders/$section',
                params: { section: value as PaymentOrdersSectionId },
                search: (previous) => previous,
              })
            }
          >
            <TabsList>
              {PAYMENT_ORDERS_SECTION_IDS.map((id) => (
                <TabsTrigger key={id} value={id}>
                  {t(PAYMENT_ORDERS_SECTION_LABELS[id])}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <div className='min-h-0 flex-1'>
            {section === 'unmatched' ? (
              <PaymentUnmatchedTable />
            ) : (
              <PaymentOrdersTable />
            )}
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
