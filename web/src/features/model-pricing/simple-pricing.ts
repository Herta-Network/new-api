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
import { flattenBinary } from '@/features/pricing/lib/billing-expression/display'
import { compileBillingExpression } from '@/features/pricing/lib/billing-expression/parser'
import type { TokenVariable } from '@/features/pricing/lib/billing-expression/types'

// The simple form edits exactly one flat tier in real USD: per 1M tokens of
// each category, or per request. Anything else stays in the advanced editor.
export const SIMPLE_PRICE_FIELDS = [
  { key: 'input', variable: 'p' },
  { key: 'output', variable: 'c' },
  { key: 'cacheRead', variable: 'cr' },
  { key: 'cacheWrite', variable: 'cc' },
  { key: 'cacheWrite1h', variable: 'cc1h' },
  { key: 'cacheWrite12h', variable: 'cc12h' },
  { key: 'imageInput', variable: 'img' },
  { key: 'audioInput', variable: 'ai' },
  { key: 'audioOutput', variable: 'ao' },
] as const satisfies readonly { key: string; variable: TokenVariable }[]

export type SimplePriceField = (typeof SIMPLE_PRICE_FIELDS)[number]['key']

export type SimplePricing = {
  unit: 'token' | 'request'
  /** Empty means the category is billed with its parent price (p or c). */
  prices: Record<SimplePriceField, string>
  requestPrice: string
  /** Tier names are kept so an unedited expression keeps its log labels. */
  tokenLabel: string
  requestLabel: string
}

export type SimplePricingIssue = {
  field: SimplePriceField | 'requestPrice'
  message: string
}

export type SimplePricingResult =
  | { ok: true; expression: string }
  | { ok: false; issues: SimplePricingIssue[] }

const NUMBER_PATTERN = /^(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/

export function emptySimplePricing(): SimplePricing {
  return {
    unit: 'token',
    prices: {
      input: '',
      output: '',
      cacheRead: '',
      cacheWrite: '',
      cacheWrite1h: '',
      cacheWrite12h: '',
      imageInput: '',
      audioInput: '',
      audioOutput: '',
    },
    requestPrice: '',
    tokenLabel: 'base',
    requestLabel: 'request',
  }
}

function priceNumber(value: string): number | null {
  const text = value.trim()
  if (!NUMBER_PATTERN.test(text)) return null
  const number = Number(text)
  return Number.isFinite(number) && number >= 0 ? number : null
}

/**
 * Lists the priced variables in canonical order, applying the conversion
 * rules of expr.md: a category priced like the input stays in p. Cache reads
 * merge only while no other input category is priced separately, because
 * overlapping counts make the clamped input remainder differ otherwise.
 */
function simpleTerms(
  pricing: SimplePricing,
  issues: SimplePricingIssue[]
): [TokenVariable, number][] {
  const values = new Map<SimplePriceField, number>()
  for (const field of SIMPLE_PRICE_FIELDS) {
    const text = pricing.prices[field.key].trim()
    if (text === '') {
      if (field.key === 'input' || field.key === 'output') {
        issues.push({ field: field.key, message: 'Enter a price.' })
      }
      continue
    }
    const number = priceNumber(text)
    if (number === null) {
      issues.push({
        field: field.key,
        message: 'Enter a finite, non-negative price.',
      })
      continue
    }
    values.set(field.key, number)
  }
  const input = values.get('input')
  if (input === undefined) return []
  if (values.get('imageInput') === input) values.delete('imageInput')
  const separateInput = [
    'cacheWrite',
    'cacheWrite1h',
    'cacheWrite12h',
    'imageInput',
    'audioInput',
  ].some((field) => values.has(field as SimplePriceField))
  if (values.get('cacheRead') === input && (input === 0 || !separateInput)) {
    values.delete('cacheRead')
  }
  return SIMPLE_PRICE_FIELDS.flatMap((field) => {
    const value = values.get(field.key)
    return value === undefined ? [] : [[field.variable, value]]
  })
}

export function buildSimplePricingExpr(
  pricing: SimplePricing
): SimplePricingResult {
  const issues: SimplePricingIssue[] = []
  if (pricing.unit === 'request') {
    const price = priceNumber(pricing.requestPrice)
    if (pricing.requestPrice.trim() === '') {
      issues.push({ field: 'requestPrice', message: 'Enter a price.' })
    } else if (price === null || !Number.isFinite(price * 1_000_000)) {
      issues.push({
        field: 'requestPrice',
        message: 'Enter a finite, non-negative price.',
      })
    }
    if (issues.length > 0 || price === null) return { ok: false, issues }
    return {
      ok: true,
      expression: `tier(${JSON.stringify(pricing.requestLabel || 'request')}, fixed(${String(price)}))`,
    }
  }
  // A long-lived write without its own price (cc1h, cc12h) is billed at a
  // fixed multiple of cc, so neither needs a price of its own.
  const terms = simpleTerms(pricing, issues)
  if (issues.length > 0) return { ok: false, issues }
  const body = terms
    .map(([variable, value]) => `${variable} * ${String(value)}`)
    .join(' + ')
  return {
    ok: true,
    expression: `tier(${JSON.stringify(pricing.tokenLabel || 'base')}, ${body})`,
  }
}

/**
 * Reads an expression the simple form can show and write back without
 * changing what it bills. Request rules, conditions, version prefixes,
 * image counts, other variables, and categories the form would merge into
 * p return null so the advanced editor keeps them intact.
 */
export function parseSimplePricingExpr(
  expression: string
): SimplePricing | null {
  const source = expression.trim()
  if (!source || /^v\d+:/.test(source)) return null
  const compiled = compileBillingExpression(source)
  if (compiled.status !== 'ready') return null
  const root = compiled.ast
  if (
    root.kind !== 'call' ||
    root.name !== 'tier' ||
    root.args[0].kind !== 'literal' ||
    typeof root.args[0].value !== 'string'
  ) {
    return null
  }
  const pricing = emptySimplePricing()
  const label = root.args[0].value
  const body = root.args[1]
  if (body.kind === 'call' && body.name === 'fixed') {
    const amount = body.args[0]
    if (amount.kind !== 'literal' || typeof amount.value !== 'number') {
      return null
    }
    pricing.unit = 'request'
    pricing.requestLabel = label
    pricing.requestPrice = String(amount.value)
    return pricing
  }
  pricing.tokenLabel = label
  const parsed = new Set<TokenVariable>()
  for (const term of flattenBinary(body, '+')) {
    if (
      term.kind !== 'binary' ||
      term.operator !== '*' ||
      term.left.kind !== 'variable' ||
      term.right.kind !== 'literal' ||
      typeof term.right.value !== 'number'
    ) {
      return null
    }
    const variable = term.left.name
    const field = SIMPLE_PRICE_FIELDS.find(
      (candidate) => candidate.variable === variable
    )
    if (!field || parsed.has(field.variable)) return null
    parsed.add(field.variable)
    pricing.prices[field.key] = String(term.right.value)
  }
  const issues: SimplePricingIssue[] = []
  const written = simpleTerms(pricing, issues)
  if (
    issues.length > 0 ||
    written.length !== parsed.size ||
    written.some(([variable]) => !parsed.has(variable))
  ) {
    return null
  }
  return pricing
}
