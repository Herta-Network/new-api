import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { UserSubscriptionsDialog } from '../dialogs/user-subscriptions-dialog'

it('explains that invalidation stops future Stripe entitlement grants and billing must be cancelled separately', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (path) => ({
    data: {
      success: true,
      data:
        path === '/api/subscription/admin/plans'
          ? []
          : [
              {
                subscription: {
                  id: 1,
                  user_id: 8,
                  plan_id: 1,
                  status: 'active',
                  end_time: 9999999999,
                  amount_total: 1000,
                  amount_used: 0,
                },
              },
            ],
    },
  }))
  const user = userEvent.setup()
  render(
    <UserSubscriptionsDialog
      open
      user={{ id: 8, username: 'customer' }}
      onOpenChange={vi.fn()}
    />
  )
  await user.click(await screen.findByRole('button', { name: 'Actions' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Invalidate' }))
  expect(
    within(await screen.findByRole('alertdialog')).getByText(
      'For Stripe subscriptions, this also stops future entitlement grants. Cancel recurring billing in Stripe to stop further charges.'
    )
  ).toBeVisible()
})
