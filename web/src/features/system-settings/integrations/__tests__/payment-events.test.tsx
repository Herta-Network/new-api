import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { PaymentEventDetails } from '../payment-event-api'
import { PaymentEventsDialog } from '../payment-events-dialog'

const details: PaymentEventDetails = {
  event: {
    id: 1,
    order_no: 'PAY-REVIEW',
    settlement_order_no: '',
    trade_no: 'in_review',
    amount: 1000,
    currency: 'USD',
    status: 'review',
    attempts: 12,
    last_error: 'retry_exhausted',
    created_at: 1700000000,
    next_attempt_at: 0,
    revision: 7,
  },
  order: {
    user_id: 8,
    provider: 'stripe',
    status: 'pending',
    renewal_stopped: false,
  },
  amount_decimal: '10.00',
  actions: [],
}

function renderEvents() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PaymentEventsDialog onOpenChange={vi.fn()} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockImplementation(async (path) => {
    if (path === '/api/option/payment-events') {
      return {
        data: { success: true, data: { items: [details.event], total: 1 } },
      }
    }
    if (path === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope: 'payment.event.review',
            methods: [{ method: 'password', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return { data: { success: true, data: details } }
  })
})

it('requires a review reason and identity verification before queuing a retry', async () => {
  const post = vi.spyOn(api, 'post').mockImplementation(async (path) => {
    if (path === '/api/verify') {
      return {
        data: {
          success: true,
          data: {
            proof_token: 'proof-for-review',
            scope: 'payment.event.review',
            method: 'password',
            expires_at: 9999999999,
          },
        },
      }
    }
    return { data: { success: true } }
  })
  const user = userEvent.setup()
  renderEvents()
  await user.click(
    await screen.findByRole('button', { name: 'Review payment' })
  )
  expect(
    await screen.findByRole('button', { name: 'Retry verification' })
  ).toBeDisabled()
  await user.type(
    screen.getByRole('textbox', { name: 'Review reason' }),
    'Merchant has confirmed payment'
  )
  await user.click(screen.getByRole('button', { name: 'Retry verification' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Continue',
    })
  )
  await screen.findByText('Security verification')
  expect(post).not.toHaveBeenCalled()
  await user.type(
    screen.getByLabelText('Password', { exact: true, selector: 'input' }),
    'operator-password'
  )
  await user.click(screen.getByRole('button', { name: 'Verify' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/option/payment-events/1/review',
      {
        revision: 7,
        action: 'retry',
        reason: 'Merchant has confirmed payment',
      },
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'proof-for-review' },
      })
    )
  )
})

it('explains that closing review does not grant credit and preserves the reason after verification is cancelled', async () => {
  const user = userEvent.setup()
  renderEvents()
  await user.click(
    await screen.findByRole('button', { name: 'Review payment' })
  )
  await user.type(
    await screen.findByRole('textbox', { name: 'Review reason' }),
    'Handled with support ticket 123'
  )
  await user.click(screen.getByRole('button', { name: 'Close review' }))
  expect(
    screen.getByText(
      'This action does not grant credit or issue a refund. Your review reason will be recorded.'
    )
  ).toBeVisible()
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Continue',
    })
  )
  const verification = await screen.findByRole('dialog', {
    name: 'Security verification',
  })
  await user.click(within(verification).getByRole('button', { name: 'Cancel' }))
  await waitFor(() =>
    expect(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Cancel',
      })
    ).toBeEnabled()
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Cancel',
    })
  )
  expect(screen.getByRole('textbox', { name: 'Review reason' })).toHaveValue(
    'Handled with support ticket 123'
  )
})

it('keeps completed payments read-only while showing their review history', async () => {
  vi.mocked(api.get).mockImplementation(async (path) => {
    if (path === '/api/option/payment-events') {
      return {
        data: { success: true, data: { items: [details.event], total: 1 } },
      }
    }
    return {
      data: {
        success: true,
        data: {
          ...details,
          event: { ...details.event, status: 'completed' },
          actions: [
            {
              id: 1,
              actor_id: 2,
              action: 'retry',
              reason: 'Verified against merchant invoice',
              created_at: 1700000000,
            },
          ],
        },
      },
    }
  })
  const user = userEvent.setup()
  renderEvents()
  await user.click(
    await screen.findByRole('button', { name: 'Review payment' })
  )
  expect(
    await screen.findByText('Verified against merchant invoice')
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Retry verification' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Close review' })).toBeDisabled()
})

it('shows empty results and applies status and order filters on the first page', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: { success: true, data: { items: [], total: 0 } },
  })
  const user = userEvent.setup()
  renderEvents()
  expect(await screen.findByText('No payment events')).toBeVisible()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Status' }),
    'pending'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Order number' }),
    'PAY-123'
  )
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(api.get).toHaveBeenCalledWith(
      '/api/option/payment-events',
      expect.objectContaining({
        params: { p: 1, page_size: 20, status: 'pending', order_no: 'PAY-123' },
      })
    )
  )
})

it('allows retrying a failed event list request', async () => {
  vi.mocked(api.get).mockRejectedValueOnce(new Error('network unavailable'))
  const user = userEvent.setup()
  renderEvents()
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('button', { name: 'Review payment' })
  ).toBeVisible()
})

it('keeps the review reason and refreshes event details when another operator changed the event', async () => {
  vi.spyOn(api, 'post').mockImplementation(async (path) => {
    if (path === '/api/verify') {
      return {
        data: {
          success: true,
          data: {
            proof_token: 'proof',
            scope: 'payment.event.review',
            method: 'password',
            expires_at: 9999999999,
          },
        },
      }
    }
    return {
      data: {
        success: false,
        message: 'Payment event changed. Reload and try again.',
      },
    }
  })
  const user = userEvent.setup()
  renderEvents()
  await user.click(
    await screen.findByRole('button', { name: 'Review payment' })
  )
  await user.type(
    await screen.findByRole('textbox', { name: 'Review reason' }),
    'Check support ticket 456'
  )
  await user.click(screen.getByRole('button', { name: 'Retry verification' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Continue',
    })
  )
  await user.type(
    await screen.findByLabelText('Password', {
      selector: 'input',
      exact: true,
    }),
    'operator-password'
  )
  await user.click(screen.getByRole('button', { name: 'Verify' }))
  await waitFor(() =>
    expect(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Continue',
      })
    ).toBeEnabled()
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Cancel',
    })
  )
  expect(screen.getByRole('textbox', { name: 'Review reason' })).toHaveValue(
    'Check support ticket 456'
  )
  expect(
    vi
      .mocked(api.get)
      .mock.calls.filter(([path]) => path === '/api/option/payment-events/1')
      .length
  ).toBeGreaterThan(1)
})
