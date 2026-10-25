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
import { render, screen, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { OrderInvoicesDialog } from '../components/order-invoices-dialog'
import type { PaymentInvoice, PaymentOrder } from '../types'

const order = {
  order_no: 'P20261025000000ABCDEFGHIJ',
  platform: 'epay',
} as PaymentOrder

function invoice(overrides: Partial<PaymentInvoice>): PaymentInvoice {
  return {
    id: 1,
    invoice_no: order.order_no,
    order_id: 7,
    payment_method_id: 3,
    platform: 'stripe',
    payment_amount: 1990,
    payment_currency: 'USD',
    exchange_rate: '1',
    trade_no: '',
    status: 1,
    close_reason: '',
    created_at: 1792900000,
    paid_at: 0,
    closed_at: 0,
    ...overrides,
  }
}

function renderDialog(invoices: PaymentInvoice[]) {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: invoices } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <OrderInvoicesDialog order={order} onClose={() => {}} />
    </QueryClientProvider>
  )
  return get
}

afterEach(() => vi.restoreAllMocks())

it('lists the invoices of an order with the payment that completed it and why the others closed', async () => {
  const get = renderDialog([
    invoice({ status: 3, close_reason: 'order_paid' }),
    invoice({
      id: 2,
      invoice_no: order.order_no + 'R2',
      platform: 'epay',
      payment_amount: 14411,
      payment_currency: 'CNY',
      trade_no: 'T2',
      status: 2,
    }),
  ])

  const dialog = await screen.findByRole('dialog', {
    name: 'Invoices of order P20261025000000ABCDEFGHIJ',
  })
  expect(get).toHaveBeenCalledWith(
    '/api/payment/admin/orders/P20261025000000ABCDEFGHIJ/invoices'
  )
  const rows = await within(dialog).findAllByRole('row')
  expect(rows).toHaveLength(3)
  expect(within(rows[1]).getByText('Stripe')).toBeVisible()
  expect(within(rows[1]).getByText('Closed')).toBeVisible()
  expect(
    within(rows[1]).getByText('The order was paid through another invoice')
  ).toBeVisible()
  expect(within(rows[2]).getByText('P20261025000000ABCDEFGHIJR2')).toBeVisible()
  expect(within(rows[2]).getByText('EPay')).toBeVisible()
  expect(within(rows[2]).getByText('T2')).toBeVisible()
  expect(within(rows[2]).getByText('Paid')).toBeVisible()
})

it('says so when an order has no invoices', async () => {
  renderDialog([])

  const dialog = await screen.findByRole('dialog')
  expect(
    await within(dialog).findByText('This order has no invoices.')
  ).toBeVisible()
  expect(within(dialog).queryByRole('table')).not.toBeInTheDocument()
})
