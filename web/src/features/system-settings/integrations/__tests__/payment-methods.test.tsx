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
import type { FormEvent } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import type { PaymentMethodData } from '../payment-method-api'
import { PaymentMethodsVisualEditor } from '../payment-methods-visual-editor'

const method: PaymentMethodData = {
  id: 1,
  version_id: 2,
  name: 'Main Alipay',
  provider: 'epay',
  icon: '',
  sort: 0,
  enabled: true,
  has_key: true,
  config: {
    endpoint: 'https://pay.example.com',
    merchant_id: '10001',
    key: '',
    payment_type: 'alipay',
  },
}

const outerSubmit = vi.fn((event: FormEvent<HTMLFormElement>) =>
  event.preventDefault()
)

function renderMethods() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <form onSubmit={outerSubmit}>
        <PaymentMethodsVisualEditor />
      </form>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [method] },
  })
})

it('requires merchant credentials when creating a method and submits a separate configuration', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true } })
  const user = userEvent.setup()
  renderMethods()
  await screen.findByText('Main Alipay')
  await user.click(screen.getByRole('button', { name: 'Add payment method' }))
  await screen.findByRole('dialog')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(post).not.toHaveBeenCalled()
  await user.type(
    screen.getByRole('textbox', { name: 'Name' }),
    'Second Alipay'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Epay endpoint' }),
    'https://second.example.com'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Epay merchant ID' }),
    '10002'
  )
  await user.type(screen.getByLabelText('Epay secret key'), 'new-test-key')
  await user.click(screen.getByRole('switch', { name: 'Enabled' }))
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/option/payment-methods',
      expect.objectContaining({
        id: 0,
        name: 'Second Alipay',
        enabled: false,
        config: expect.objectContaining({
          merchant_id: '10002',
          key: 'new-test-key',
          payment_type: 'alipay',
        }),
      })
    )
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(outerSubmit).not.toHaveBeenCalled()
})

it('retains the edit form after save failure and allows keeping the existing secret', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValueOnce({ data: { success: false, message: 'Save failed' } })
    .mockResolvedValueOnce({ data: { success: true } })
  const user = userEvent.setup()
  renderMethods()
  await screen.findByText('Main Alipay')
  await user.click(screen.getByRole('button', { name: 'Edit payment method' }))
  await screen.findByRole('dialog')
  expect(screen.getByLabelText('Epay secret key')).toHaveValue('')
  const name = screen.getByRole('textbox', { name: 'Name' })
  await user.clear(name)
  await user.type(name, 'Updated Alipay')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  )
  expect(name).toHaveValue('Updated Alipay')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(put).toHaveBeenLastCalledWith(
      '/api/option/payment-methods/1',
      expect.objectContaining({
        version_id: 2,
        config: expect.objectContaining({ key: '' }),
      })
    )
  )
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(outerSubmit).not.toHaveBeenCalled()
})

it('shows an empty list and recovers a failed list request through Retry', async () => {
  vi.mocked(api.get)
    .mockRejectedValueOnce(new Error('Offline'))
    .mockResolvedValueOnce({ data: { success: true, data: [] } })
  const user = userEvent.setup()
  renderMethods()
  await user.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No payment methods configured')).toBeVisible()
})

it('requires confirmation before deleting a method', async () => {
  const remove = vi
    .spyOn(api, 'delete')
    .mockResolvedValue({ data: { success: true } })
  const user = userEvent.setup()
  renderMethods()
  await screen.findByText('Main Alipay')
  await user.click(screen.getByRole('button', { name: 'Actions' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
  expect(await screen.findByRole('alertdialog')).toBeVisible()
  expect(remove).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await waitFor(() =>
    expect(remove).toHaveBeenCalledWith('/api/option/payment-methods/1')
  )
})
