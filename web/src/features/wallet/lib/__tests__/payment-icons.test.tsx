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
import { render, screen } from '@testing-library/react'
import { expect, test } from 'vitest'

import { PAYMENT_PLATFORMS, STRIPE_ICON_COLOR } from '../../constants'
import { getPaymentIcon } from '../ui'

test('the built-in Stripe icon preserves the requested size and brand color', () => {
  render(getPaymentIcon(PAYMENT_PLATFORMS.STRIPE, 'h-6 w-6'))
  const icon = screen.getByRole('img', { name: 'Stripe' })
  expect(icon).toHaveClass('h-6', 'w-6')
  expect(icon).toHaveStyle({ color: STRIPE_ICON_COLOR })
})

test('an admin-configured Simple Icon still resolves through the lazy icon loader', async () => {
  render(
    getPaymentIcon(
      PAYMENT_PLATFORMS.EPAY,
      'h-4 w-4',
      'SiAlipay',
      'Configured Alipay'
    )
  )
  const title = await screen.findByTitle('Configured Alipay')
  expect(title.closest('svg')).toHaveClass('h-4', 'w-4')
})
