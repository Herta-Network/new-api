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
import { describe, expect, it } from 'vitest'

import {
  buildSimplePricingExpr,
  emptySimplePricing,
  parseSimplePricingExpr,
  type SimplePriceField,
  type SimplePricing,
} from '../simple-pricing'

function tokenPricing(
  prices: Partial<Record<SimplePriceField, string>>
): SimplePricing {
  const pricing = emptySimplePricing()
  Object.assign(pricing.prices, prices)
  return pricing
}

function expressionOf(pricing: SimplePricing) {
  const result = buildSimplePricingExpr(pricing)
  if (!result.ok) throw new Error(JSON.stringify(result.issues))
  return result.expression
}

describe('simple pricing form to expression', () => {
  it('writes the canonical tier in real USD per 1M tokens and reads it back', () => {
    const pricing = tokenPricing({
      input: '2.5',
      output: '15',
      cacheRead: '0.25',
    })
    const expression = expressionOf(pricing)
    expect(expression).toBe('tier("base", p * 2.5 + c * 15 + cr * 0.25)')
    expect(parseSimplePricingExpr(expression)).toEqual(pricing)
  })

  it('leaves image input in p when it costs the same as input', () => {
    expect(
      expressionOf(tokenPricing({ input: '2', output: '8', imageInput: '2' }))
    ).toBe('tier("base", p * 2 + c * 8)')
    expect(
      expressionOf(tokenPricing({ input: '2', output: '8', imageInput: '0' }))
    ).toBe('tier("base", p * 2 + c * 8 + img * 0)')
  })

  it('merges same-price cache reads only while no other input category is priced separately', () => {
    expect(
      expressionOf(tokenPricing({ input: '2', output: '8', cacheRead: '2' }))
    ).toBe('tier("base", p * 2 + c * 8)')
    expect(
      expressionOf(
        tokenPricing({
          input: '2',
          output: '8',
          cacheRead: '2',
          cacheWrite: '2.5',
        })
      )
    ).toBe('tier("base", p * 2 + c * 8 + cr * 2 + cc * 2.5)')
    expect(
      expressionOf(
        tokenPricing({
          input: '0',
          output: '1',
          cacheRead: '0',
          imageInput: '3',
        })
      )
    ).toBe('tier("base", p * 0 + c * 1 + img * 3)')
  })

  it('splits Claude cache writes into 5-minute and 1-hour prices', () => {
    const pricing = tokenPricing({
      input: '3',
      output: '15',
      cacheRead: '0.3',
      cacheWrite: '3.75',
      cacheWrite1h: '6',
    })
    const expression = expressionOf(pricing)
    expect(expression).toBe(
      'tier("base", p * 3 + c * 15 + cr * 0.3 + cc * 3.75 + cc1h * 6)'
    )
    expect(parseSimplePricingExpr(expression)).toEqual(pricing)
  })

  it('leaves a long-lived cache write price optional', () => {
    // The backend bills an unpriced 1h write at 1.6x cc and a 12h write at 2x.
    expect(
      expressionOf(
        tokenPricing({ input: '3', output: '15', cacheWrite: '3.75' })
      )
    ).toBe('tier("base", p * 3 + c * 15 + cc * 3.75)')
  })

  it('round-trips an OpenAI 12-hour cache write price', () => {
    const expression =
      'tier("base", p * 10 + c * 50 + cr * 1 + cc * 12.5 + cc12h * 25)'
    const pricing = parseSimplePricingExpr(expression)
    expect(pricing?.prices).toMatchObject({
      input: '10',
      output: '50',
      cacheRead: '1',
      cacheWrite: '12.5',
      cacheWrite12h: '25',
    })
    expect(pricing && expressionOf(pricing)).toBe(expression)
  })

  it('keeps audio prices as their own terms', () => {
    const pricing = tokenPricing({
      input: '2',
      output: '2',
      audioInput: '4',
      audioOutput: '4',
    })
    const expression = expressionOf(pricing)
    expect(expression).toBe('tier("base", p * 2 + c * 2 + ai * 4 + ao * 4)')
    expect(parseSimplePricingExpr(expression)).toEqual(pricing)
  })

  it('writes a fixed request price, including an explicit zero', () => {
    const pricing = { ...emptySimplePricing(), unit: 'request' as const }
    pricing.requestPrice = '0.01'
    expect(expressionOf(pricing)).toBe('tier("request", fixed(0.01))')
    expect(parseSimplePricingExpr('tier("request", fixed(0.01))')).toEqual(
      pricing
    )
    pricing.requestPrice = '0'
    expect(expressionOf(pricing)).toBe('tier("request", fixed(0))')
  })

  it('requires input, output and request prices instead of assuming zero', () => {
    expect(buildSimplePricingExpr(tokenPricing({ output: '1' }))).toEqual({
      ok: false,
      issues: [{ field: 'input', message: 'Enter a price.' }],
    })
    expect(
      buildSimplePricingExpr(
        tokenPricing({ input: '1', output: '1', cacheRead: '-1' })
      )
    ).toEqual({
      ok: false,
      issues: [
        { field: 'cacheRead', message: 'Enter a finite, non-negative price.' },
      ],
    })
    expect(
      buildSimplePricingExpr({ ...emptySimplePricing(), unit: 'request' })
    ).toEqual({
      ok: false,
      issues: [{ field: 'requestPrice', message: 'Enter a price.' }],
    })
  })

  it('keeps tier names so unedited log labels do not change', () => {
    const pricing = parseSimplePricingExpr(
      'tier("standard", p * 1 + cr * 0.1 + c * 2)'
    )
    expect(pricing?.prices).toMatchObject({
      input: '1',
      output: '2',
      cacheRead: '0.1',
    })
    expect(pricing && expressionOf(pricing)).toBe(
      'tier("standard", p * 1 + c * 2 + cr * 0.1)'
    )
  })

  it.each([
    'tier("base", p * 4 + c * 16)',
    'tier("base", p * 4 + c * 12 + cr * 0 + cc * 6 + img * 8)',
    'tier("base", p * 4 + c * 4 + cr * 4 + img * 0)',
    'tier("base", p * 60 + c * 120 + cr * 60 + cc * 75)',
    'tier("base", p * 4 + c * 0 + cr * 4 + cc * 5 + cc1h * 8)',
    'tier("request", fixed(0.25))',
  ])('opens converted legacy prices in the simple form: %s', (expression) => {
    const pricing = parseSimplePricingExpr(expression)
    expect(pricing).not.toBeNull()
    expect(pricing && expressionOf(pricing)).toBe(expression)
  })
})

describe('expressions only the advanced editor can represent', () => {
  it.each([
    [
      'context tiers',
      'len <= 200000 ? tier("standard", p * 3 + c * 15) : tier("long_context", p * 6 + c * 22.5)',
    ],
    [
      'request rules',
      '(tier("base", p * 5 + c * 25)) * (param("service_tier") == "fast" ? 2 : 1)',
    ],
    ['image counts', 'tier("image", fixed(1)) * image_count'],
    [
      'image cache tokens',
      'tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)',
    ],
    ['image output tokens', 'tier("base", p * 1 + c * 2 + img_o * 3)'],
    ['a version prefix', 'v1:tier("base", p * 1 + c * 2)'],
    ['functions', 'tier("base", max(p, 100) * 1 + c * 2)'],
    ['a repeated variable', 'tier("base", p * 1 + p * 2 + c * 2)'],
    ['a coefficient before the variable', 'tier("base", 2 * p + c * 2)'],
    ['a missing output price', 'tier("base", p * 2)'],
    [
      'a cache read the form would merge',
      'tier("base", p * 2 + c * 8 + cr * 2)',
    ],
    [
      'an image input the form would merge',
      'tier("base", p * 2 + c * 8 + img * 2)',
    ],
    [
      'a free cache read on a free input',
      'tier("base", p * 0 + c * 0 + cr * 0)',
    ],
    ['an invalid expression', 'tier("base", p * )'],
  ])('does not read %s as simple prices', (_name, expression) => {
    expect(parseSimplePricingExpr(expression)).toBeNull()
  })
})
