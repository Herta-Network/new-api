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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect, useState, type ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { Wallet } from '..'
import type {
  CheckoutAction,
  PaymentMethodOption,
  PaymentOrder,
  PaymentQuote,
} from '../types'

// Stripe.js is the external boundary: the Payment Element lives in Stripe's
// iframe, so the test stands in for it and records the confirmation.
const stripe = vi.hoisted(() => ({ confirmPayment: vi.fn() }))

vi.mock('@stripe/stripe-js', () => ({
  loadStripe: vi.fn(async () => stripe),
}))

vi.mock('@stripe/react-stripe-js', () => ({
  // Like Stripe.js, refuse a malformed client secret while rendering.
  Elements: (props: {
    children: ReactNode
    options: { clientSecret: string }
  }) => {
    if (!props.options.clientSecret.includes('_secret_')) {
      throw new Error('Invalid value for elements(): clientSecret')
    }
    return props.children
  },
  PaymentElement: function PaymentElement(props: { onReady?: () => void }) {
    const onReady = props.onReady
    useEffect(() => onReady?.(), [onReady])
    return <div>Stripe Payment Element</div>
  },
  useStripe: () => stripe,
  useElements: () => ({ elements: 'stripe' }),
}))

const info = {
  currency: 'USD',
  min_topup: '1',
  amount_options: [10, 50],
  amount_discount: {},
  payment_compliance_confirmed: true,
  payment_compliance_terms_version: 'v1',
}

function method(
  id: number,
  name: string,
  platform: PaymentMethodOption['platform'],
  extra: Partial<PaymentMethodOption> = {}
): PaymentMethodOption {
  return {
    id,
    name,
    platform,
    icon: '',
    description: '',
    fee_mode: 0,
    fee_rate_bp: 0,
    fee_fixed: 0,
    charge_currency: 'USD',
    available: true,
    ...extra,
  }
}

const alipay = method(3, 'Alipay', 'epay', { charge_currency: 'CNY' })
const card = method(4, 'Card', 'stripe')
const balance = method(-1, 'Balance', 'balance')

const topupQuote: PaymentQuote = {
  purpose: 'topup',
  currency: 'USD',
  subject: 'Top-up 12.50 USD',
  topup_amount: 1250,
  plan_id: 0,
  price: 1250,
  adjustment: -125,
  fee_amount: 30,
  amount: 1155,
  quota_amount: 6250000,
  quota_rate: '1',
  payment_amount: 8300,
  payment_currency: 'CNY',
}

function order(
  orderNo: string,
  status: PaymentOrder['status'],
  extra: Partial<PaymentOrder> = {}
): PaymentOrder {
  return {
    id: 1,
    order_no: orderNo,
    user_id: 1,
    purpose: 'topup',
    plan_id: 0,
    subject: 'Top-up 12.50 USD',
    topup_amount: 1250,
    quota_amount: 6250000,
    quota_rate: '1',
    currency: 'USD',
    price: 1250,
    adjustment: 0,
    fee_amount: 0,
    amount: 1250,
    payment_method_id: 4,
    platform: 'stripe',
    payment_amount: 1250,
    payment_currency: 'USD',
    exchange_rate: '1',
    trade_no: '',
    status,
    fulfil_error: '',
    user_subscription_id: 0,
    close_reason: '',
    created_at: 1,
    expires_at: Math.floor(Date.now() / 1000) + 900,
    paid_at: 0,
    finished_at: 0,
    closed_at: 0,
    ...extra,
  }
}

interface ServerFixture {
  methods?: Record<string, PaymentMethodOption[]>
  quote?: PaymentQuote | Record<string, unknown>
  checkout?: CheckoutAction
  orders?: Record<string, PaymentOrder>
  synced?: PaymentOrder
}

/** Answer the wallet's requests from the fixture and record every POST. */
function mockServer(fixture: ServerFixture) {
  vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    const ok = (data: unknown) => ({ data: { success: true, data } })
    if (url === '/api/payment/info') return ok(info)
    if (url === '/api/payment/methods') {
      const params = config?.params as { purpose?: string } | undefined
      return ok(fixture.methods?.[params?.purpose ?? ''] ?? [])
    }
    if (url === '/api/user/self') {
      return ok({ id: 1, username: 'buyer', quota: 10000000, aff_quota: 0 })
    }
    if (url === '/api/subscription/plans') {
      return ok([
        {
          plan: {
            id: 7,
            title: 'Pro',
            price_amount: 9.99,
            duration_unit: 'month',
            duration_value: 1,
            quota_reset_period: 'never',
            enabled: true,
            sort_order: 0,
            max_purchase_per_user: 0,
            total_amount: 0,
          },
        },
      ])
    }
    if (url === '/api/subscription/self') {
      return ok({
        billing_preference: 'subscription_first',
        subscriptions: [],
        all_subscriptions: [],
      })
    }
    const orderNo = url.match(/^\/api\/payment\/orders\/([^/]+)$/)?.[1]
    if (orderNo && fixture.orders?.[orderNo]) {
      return ok(fixture.orders[orderNo])
    }
    return ok(null)
  })
  return vi.spyOn(api, 'post').mockImplementation(async (url) => {
    if (url === '/api/payment/quote') {
      const quote = fixture.quote
      return {
        data:
          quote && 'success' in quote ? quote : { success: true, data: quote },
      }
    }
    if (url === '/api/payment/orders' && fixture.checkout) {
      return {
        data: {
          success: true,
          data: {
            order: order(fixture.checkout.order_no, 1),
            checkout: fixture.checkout,
          },
        },
      }
    }
    if (url.endsWith('/sync') && fixture.synced) {
      return { data: { success: true, data: fixture.synced } }
    }
    return { data: { success: false, message: 'unexpected request' } }
  })
}

function WalletHarness(props: { initialOrder?: string }) {
  const [paymentOrder, setPaymentOrder] = useState(props.initialOrder)
  return (
    <Wallet
      paymentOrder={paymentOrder}
      onPaymentOrderChange={setPaymentOrder}
    />
  )
}

function renderWallet(initialOrder?: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <WalletHarness initialOrder={initialOrder} />
    </QueryClientProvider>
  )
}

function postsTo(post: ReturnType<typeof mockServer>, url: string) {
  return post.mock.calls.filter((call) => call[0] === url).map((c) => c[1])
}

beforeEach(() => {
  stripe.confirmPayment.mockReset()
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

it('quotes a typed top-up with the chosen method and opens the payment page of a URL checkout', async () => {
  const assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, assign })
  const post = mockServer({
    methods: { topup: [card, alipay] },
    quote: topupQuote,
    checkout: {
      type: 'url',
      url: 'https://pay.example/submit.php?sign=x',
      order_no: 'P1',
    },
  })
  renderWallet()

  await userEvent.click(await screen.findByRole('button', { name: /^Alipay/ }))
  const amount = screen.getByRole('textbox', { name: /Custom Amount/ })
  await userEvent.clear(amount)
  await userEvent.type(amount, '12.5')

  const summary = await screen.findByLabelText('Price details')
  expect(within(summary).getByText('$12.50')).toBeVisible()
  expect(within(summary).getByText('-$1.25')).toBeVisible()
  expect(within(summary).getByText('+$0.30')).toBeVisible()
  expect(within(summary).getByText('$11.55')).toBeVisible()
  expect(within(summary).getByText('CN¥83.00')).toBeVisible()
  expect(postsTo(post, '/api/payment/quote').at(-1)).toEqual({
    purpose: 'topup',
    payment_method_id: 3,
    amount: 12.5,
  })

  await userEvent.click(screen.getByRole('button', { name: 'Pay $11.55' }))
  await waitFor(() =>
    expect(assign).toHaveBeenCalledWith('https://pay.example/submit.php?sign=x')
  )
  expect(postsTo(post, '/api/payment/orders')).toEqual([
    { purpose: 'topup', payment_method_id: 3, amount: 12.5 },
  ])
})

it('lists an unavailable method disabled with the server reason', async () => {
  mockServer({
    methods: {
      topup: [
        alipay,
        method(5, 'Crypto', 'cryptomus', {
          available: false,
          unavailable: { key: 'Exchange rate unavailable' },
        }),
      ],
    },
    quote: topupQuote,
  })
  renderWallet()

  const crypto = await screen.findByRole('button', { name: /^Crypto/ })
  expect(crypto).toBeDisabled()
  expect(crypto).toHaveTextContent('Exchange rate unavailable')
  expect(screen.getByRole('button', { name: /^Alipay/ })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
})

it('shows why a quote failed and does not offer to pay', async () => {
  mockServer({
    methods: { topup: [alipay] },
    quote: {
      success: false,
      message: 'Top-up amount cannot be less than 20.00 USD',
      message_key: 'Top-up amount cannot be less than {{min}}',
      message_params: { min: '20.00 USD' },
    },
  })
  renderWallet()

  expect(
    await screen.findByText('Top-up amount cannot be less than 20.00 USD')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Pay' })).toBeDisabled()
})

it('confirms a Stripe checkout in the Payment Element and then shows the settled order', async () => {
  stripe.confirmPayment.mockResolvedValue({
    paymentIntent: { status: 'succeeded' },
  })
  const post = mockServer({
    methods: { topup: [card] },
    quote: { ...topupQuote, payment_amount: 1155, payment_currency: 'USD' },
    checkout: {
      type: 'stripe',
      stripe: { publishable_key: 'pk_test_1', client_secret: 'pi_1_secret_a' },
      order_no: 'P2',
    },
    orders: { P2: order('P2', 5) },
  })
  renderWallet()

  await userEvent.click(
    await screen.findByRole('button', { name: 'Pay $11.55' })
  )
  const dialog = await screen.findByRole('dialog', { name: 'Pay with Stripe' })
  await userEvent.click(within(dialog).getByRole('button', { name: 'Pay now' }))

  expect(stripe.confirmPayment).toHaveBeenCalledWith({
    elements: { elements: 'stripe' },
    confirmParams: {
      return_url: `${window.location.origin}/wallet?payment_order=P2`,
    },
    redirect: 'if_required',
  })
  const result = await screen.findByRole('dialog', { name: 'Payment status' })
  expect(await within(result).findByText('Payment successful')).toBeVisible()
  expect(
    screen.queryByRole('dialog', { name: 'Pay with Stripe' })
  ).not.toBeInTheDocument()
  expect(postsTo(post, '/api/payment/orders')).toEqual([
    { purpose: 'topup', payment_method_id: 4, amount: 10 },
  ])
})

it('keeps the Stripe dialog open with the decline reason when the card is declined', async () => {
  stripe.confirmPayment.mockResolvedValue({
    error: { message: 'Your card was declined.' },
  })
  mockServer({
    methods: { topup: [card] },
    quote: topupQuote,
    checkout: {
      type: 'stripe',
      stripe: { publishable_key: 'pk_test_1', client_secret: 'pi_2_secret_b' },
      order_no: 'P5',
    },
  })
  renderWallet()

  await userEvent.click(
    await screen.findByRole('button', { name: 'Pay $11.55' })
  )
  const dialog = await screen.findByRole('dialog', { name: 'Pay with Stripe' })
  await userEvent.click(within(dialog).getByRole('button', { name: 'Pay now' }))

  expect(
    await within(dialog).findByText('Your card was declined.')
  ).toBeVisible()
  expect(
    screen.queryByRole('dialog', { name: 'Payment status' })
  ).not.toBeInTheDocument()
})

it('keeps a client secret Stripe rejects inside the Stripe dialog', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {})
  mockServer({
    methods: { topup: [card] },
    quote: topupQuote,
    checkout: {
      type: 'stripe',
      stripe: { publishable_key: 'pk_test_1', client_secret: 'malformed' },
      order_no: 'P6',
    },
  })
  renderWallet()

  await userEvent.click(
    await screen.findByRole('button', { name: 'Pay $11.55' })
  )
  const dialog = await screen.findByRole('dialog', { name: 'Pay with Stripe' })
  expect(
    await within(dialog).findByText(
      'The payment form could not be loaded. Try again later.'
    )
  ).toBeVisible()
  await userEvent.keyboard('{Escape}')
  expect(
    await screen.findByRole('button', { name: 'Pay $11.55' })
  ).toBeVisible()
})

it('buys a subscription from the balance without a gateway', async () => {
  const post = mockServer({
    methods: { subscription: [balance, card] },
    quote: {
      purpose: 'subscription',
      currency: 'USD',
      subject: 'Pro',
      topup_amount: 0,
      plan_id: 7,
      price: 999,
      adjustment: 0,
      fee_amount: 0,
      amount: 999,
      quota_amount: 4995000,
      quota_rate: '1',
      payment_amount: 0,
      payment_currency: '',
    },
    checkout: { type: 'paid', order_no: 'P3' },
    orders: {
      P3: order('P3', 5, { purpose: 'subscription', platform: 'balance' }),
    },
  })
  renderWallet()

  await userEvent.click(
    await screen.findByRole('button', { name: 'Subscribe Now' })
  )
  const purchase = await screen.findByRole('dialog', {
    name: 'Purchase Subscription',
  })
  const summary = await within(purchase).findByLabelText('Price details')
  expect(within(summary).getByText('Balance deducted')).toBeVisible()
  await userEvent.click(
    within(purchase).getByRole('button', { name: 'Pay $9.99' })
  )

  const result = await screen.findByRole('dialog', { name: 'Payment status' })
  expect(
    await within(result).findByText('Your subscription is active.')
  ).toBeVisible()
  expect(postsTo(post, '/api/payment/orders')).toEqual([
    { purpose: 'subscription', plan_id: 7, payment_method_id: -1 },
  ])
  expect(
    screen.queryByRole('dialog', { name: 'Purchase Subscription' })
  ).not.toBeInTheDocument()
})

it('asks the gateway once about a pending order the buyer returns to and shows it settled', async () => {
  const post = mockServer({
    orders: { P4: order('P4', 1) },
    synced: order('P4', 5),
  })
  renderWallet('P4')

  const result = await screen.findByRole('dialog', { name: 'Payment status' })
  expect(await within(result).findByText('Payment successful')).toBeVisible()
  expect(postsTo(post, '/api/payment/orders/P4/sync')).toHaveLength(1)
})
