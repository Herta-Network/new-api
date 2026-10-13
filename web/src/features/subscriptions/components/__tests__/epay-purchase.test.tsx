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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { subscriptionPlanSchema } from '../../types'
import { SubscriptionPurchaseDialog } from '../dialogs/subscription-purchase-dialog'

it('shows the CNY estimate and submits the selected configured payment method', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'Payment request failed' },
  })
  const user = userEvent.setup()
  const plan = subscriptionPlanSchema.parse({
    id: 1,
    title: 'Monthly',
    price_amount: 10,
    duration_unit: 'month',
    duration_value: 1,
    quota_reset_period: 'never',
    enabled: true,
    sort_order: 0,
    max_purchase_per_user: 0,
    total_amount: 1000,
  })
  render(
    <QueryClientProvider client={new QueryClient()}>
      <SubscriptionPurchaseDialog
        open
        onOpenChange={vi.fn()}
        plan={{ plan }}
        enableOnlineTopUp
        epayMethods={[
          { type: 'epay:2', name: 'Merchant A' },
          { type: 'epay:3', name: 'Merchant B' },
        ]}
        epayExchangeRate={7.3}
      />
    </QueryClientProvider>
  )
  expect(screen.getByText('Estimated payment: 73 CNY')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Pay' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/api/subscription/epay/pay', {
      plan_id: 1,
      payment_method: 'epay:2',
    })
  )
  expect(screen.getByRole('dialog')).toBeVisible()
})
