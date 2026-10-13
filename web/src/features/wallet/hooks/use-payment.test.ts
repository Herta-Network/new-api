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
import { act, renderHook } from '@testing-library/react'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { usePayment, requestPaymentAmount } from './use-payment'

test.each(['epay:1', 'stripe:2', 'cryptomus:3'])(
  'quotes %s through the shared endpoint with its exact method ID',
  async (method) => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        message: 'success',
        data: { amount: '18.75', currency: 'USD' },
      },
    })
    expect(await requestPaymentAmount(120, method)).toEqual({
      amount: '18.75',
      currency: 'USD',
    })
    expect(post).toHaveBeenCalledWith(
      '/api/user/amount',
      { amount: 120, payment_method: method },
      expect.anything()
    )
  }
)
test('rejects failed quotes instead of confirming a zero payment', async () => {
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: false, message: 'Unavailable' },
  })
  await expect(requestPaymentAmount(10, 'stripe:2')).rejects.toMatchObject({
    message: 'Unavailable',
  })
})

test('keeps the latest quote when an older provider response finishes later', async () => {
  let finishFirst!: (value: {
    data: { message: string; data: { amount: string; currency: string } }
  }) => void
  const firstResponse = new Promise<{
    data: { message: string; data: { amount: string; currency: string } }
  }>((resolve) => {
    finishFirst = resolve
  })
  vi.spyOn(api, 'post')
    .mockReturnValueOnce(firstResponse as ReturnType<typeof api.post>)
    .mockResolvedValueOnce({
      data: { message: 'success', data: { amount: '2.00', currency: 'USD' } },
    })
  const { result } = renderHook(() => usePayment())
  let first!: Promise<boolean>
  act(() => {
    first = result.current.calculatePaymentAmount(2, 'epay:1')
  })
  await act(async () => {
    await result.current.calculatePaymentAmount(2, 'cryptomus:2')
  })
  await act(async () => {
    finishFirst({
      data: { message: 'success', data: { amount: '14.60', currency: 'CNY' } },
    })
    await first
  })
  expect(result.current.amount).toBe(2)
  expect(result.current.currency).toBe('USD')
})
