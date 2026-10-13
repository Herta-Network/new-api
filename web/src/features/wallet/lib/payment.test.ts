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
import { expect, test } from 'vitest'

import type { TopupInfo } from '../types'
import { getDefaultPaymentType, isStripePayment } from './payment'

test('keeps configured method identities and excludes subscription-only methods from default top-up', () => {
  expect(isStripePayment('stripe:12')).toBe(true)
  expect(isStripePayment('cryptomus:12')).toBe(false)
  const info = {
    pay_methods: [
      { name: 'Subscriptions', type: 'stripe:1', topup_enabled: false },
      { name: 'Crypto', type: 'cryptomus:2' },
    ],
  } as TopupInfo
  expect(getDefaultPaymentType(info)).toBe('cryptomus:2')
  expect(getDefaultPaymentType(null)).toBe('')
})
