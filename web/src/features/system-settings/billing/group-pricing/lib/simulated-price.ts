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
import { QUOTA_TYPE_VALUES } from '@/features/pricing/constants'
import { isDynamicPricingModel } from '@/features/pricing/lib/dynamic-price'
import { calculateTokenPrice } from '@/features/pricing/lib/price'
import type { PricingModel } from '@/features/pricing/types'

// A model's listed price in USD, before the group ratio, read the way
// relay/helper.ModelPriceHelper bills it: an expression wins, then a fixed
// per-request price, then the token ratios.
export type ModelBasePrice =
  | { kind: 'token'; input: number; output: number }
  | { kind: 'request'; price: number }
  | { kind: 'expression' }

export function modelBasePrice(model: PricingModel): ModelBasePrice {
  if (isDynamicPricingModel(model)) return { kind: 'expression' }
  if (model.quota_type === QUOTA_TYPE_VALUES.REQUEST) {
    return { kind: 'request', price: model.model_price ?? 0 }
  }
  return {
    kind: 'token',
    input: calculateTokenPrice(model, 'input', 1),
    output: calculateTokenPrice(model, 'output', 1),
  }
}

// Every price of a call is the listed price × the one group ratio.
export function withGroupRatio(
  price: ModelBasePrice,
  ratio: number
): ModelBasePrice {
  if (price.kind === 'token') {
    return {
      kind: 'token',
      input: price.input * ratio,
      output: price.output * ratio,
    }
  }
  if (price.kind === 'request') {
    return { kind: 'request', price: price.price * ratio }
  }
  return price
}
