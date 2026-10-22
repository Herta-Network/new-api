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
/**
 * Currency formatting
 *
 * Quota is stored in an internal unit (`quotaPerUnit` of it make one USD).
 * Every amount shown to users and administrators is in the site currency,
 * the currency payments are priced in, converted at the same resolved rate
 * payments use (`/api/status` `site_currency.usd_rate`). While the site
 * currency has no usable rate, amounts show in USD rather than at a guessed
 * rate. In the tokens display mode, quota shows as raw quota instead.
 *
 * - `formatQuotaWithCurrency(quota)`: raw quota (balances, usage, logs)
 * - `formatCurrencyFromUSD(usd)`: an amount in USD, such as a model price
 *   or a quota converted with `quotaPerUnit`
 * - `formatBillingCurrencyFromUSD(usd)`: like `formatCurrencyFromUSD`, but
 *   always money, never raw quota (pricing pages)
 * - `formatMinorAmount(minor, currency)`: payment amounts, which are already
 *   in a named currency and never converted
 *
 * Pass interface languages through `toIntlLocale` before `options.locale`.
 */
import {
  useSystemConfigStore,
  DEFAULT_CURRENCY_CONFIG,
  type CurrencyConfig,
} from '@/stores/system-config-store'

export interface CurrencyFormatOptions {
  /** Fraction digits to use when |value| >= 1 */
  digitsLarge?: number
  /** Fraction digits to use when |value| < 1 */
  digitsSmall?: number
  /** Whether to abbreviate thousands with k suffix */
  abbreviate?: boolean
  /** Minimal absolute value to display when rounding would produce zero */
  minimumNonZero?: number
  /**
   * Use locale-aware compact notation for large values (e.g. "$28万" in zh,
   * "$280K" in en). The currency symbol is preserved.
   */
  compact?: boolean
  /** Whether to include the currency symbol. Token displays are unchanged. */
  showSymbol?: boolean
  /** Locale used for number formatting (defaults to the runtime locale) */
  locale?: Intl.LocalesArgument | undefined
}

type ResolvedCurrencyFormatOptions = Omit<
  Required<CurrencyFormatOptions>,
  'locale'
> & {
  locale: Intl.LocalesArgument | undefined
}

type MoneyDisplayMeta = {
  kind: 'currency'
  symbol: string
  currencyCode: string
  /** One USD in currencyCode */
  exchangeRate: number
}

type DisplayMeta =
  | MoneyDisplayMeta
  | {
      kind: 'tokens'
      /** Number of tokens per USD */
      quotaPerUnit: number
    }

const DEFAULT_FORMAT_OPTIONS: ResolvedCurrencyFormatOptions = {
  digitsLarge: 2,
  digitsSmall: 4,
  abbreviate: true,
  minimumNonZero: 0,
  compact: false,
  showSymbol: true,
  locale: undefined,
}

const USD_DISPLAY_META: MoneyDisplayMeta = {
  kind: 'currency',
  symbol: '$',
  currencyCode: 'USD',
  exchangeRate: 1,
}

function getConfig(): CurrencyConfig {
  const { config } = useSystemConfigStore.getState()
  const currency = config?.currency ?? DEFAULT_CURRENCY_CONFIG
  return {
    ...DEFAULT_CURRENCY_CONFIG,
    ...currency,
    quotaPerUnit:
      currency?.quotaPerUnit && currency.quotaPerUnit > 0
        ? currency.quotaPerUnit
        : DEFAULT_CURRENCY_CONFIG.quotaPerUnit,
  }
}

/**
 * The currency money shows in, also in the tokens display mode: the site
 * currency with its symbol and rate (one USD in it), or USD while the site
 * currency has no usable rate.
 */
export function getMoneyDisplay(
  config: CurrencyConfig = getConfig()
): MoneyDisplayMeta {
  const rate = config.usdRate
  if (
    typeof config.siteCurrency !== 'string' ||
    !config.siteCurrency ||
    config.siteCurrency === 'USD' ||
    rate == null ||
    !Number.isFinite(rate) ||
    rate <= 0
  ) {
    return USD_DISPLAY_META
  }
  return {
    kind: 'currency',
    symbol: config.siteCurrencySymbol || config.siteCurrency,
    currencyCode: config.siteCurrency,
    exchangeRate: rate,
  }
}

function getDisplayMeta(config: CurrencyConfig): DisplayMeta {
  if (config.quotaDisplayMode === 'tokens') {
    return { kind: 'tokens', quotaPerUnit: config.quotaPerUnit }
  }
  return getMoneyDisplay(config)
}

function mergeOptions(
  options?: CurrencyFormatOptions
): ResolvedCurrencyFormatOptions {
  if (!options) return DEFAULT_FORMAT_OPTIONS
  return {
    digitsLarge: options.digitsLarge ?? DEFAULT_FORMAT_OPTIONS.digitsLarge,
    digitsSmall: options.digitsSmall ?? DEFAULT_FORMAT_OPTIONS.digitsSmall,
    abbreviate: options.abbreviate ?? DEFAULT_FORMAT_OPTIONS.abbreviate,
    minimumNonZero:
      options.minimumNonZero ?? DEFAULT_FORMAT_OPTIONS.minimumNonZero,
    compact: options.compact ?? DEFAULT_FORMAT_OPTIONS.compact,
    showSymbol: options.showSymbol ?? DEFAULT_FORMAT_OPTIONS.showSymbol,
    locale: options.locale ?? DEFAULT_FORMAT_OPTIONS.locale,
  }
}

function getFractionDigits(
  value: number,
  digitsLarge: number,
  digitsSmall: number
): number {
  return Math.abs(value) >= 1 ? digitsLarge : digitsSmall
}

/** Return the configured fraction digits for a plain currency value. */
export function getCurrencyFractionDigits(
  value: number,
  options?: CurrencyFormatOptions
): number {
  const merged = mergeOptions(options)
  return getFractionDigits(value, merged.digitsLarge, merged.digitsSmall)
}

function removeTrailingZeros(str: string): string {
  if (!str.includes('.')) return str
  return str.replace(/(\.[0-9]*?)0+$/, '$1').replace(/\.$/, '')
}

function formatNumberWithSuffix(
  value: number,
  digitsLarge: number,
  digitsSmall: number,
  abbreviate: boolean
): string {
  const abs = Math.abs(value)
  if (abbreviate && abs >= 1000) {
    const result = value / 1000
    return `${removeTrailingZeros(result.toFixed(1))}k`
  }

  const digits = getFractionDigits(value, digitsLarge, digitsSmall)
  return removeTrailingZeros(value.toFixed(digits))
}

function adjustForMinimum(
  value: number,
  digits: number,
  minimumNonZero: number
): number {
  if (value === 0) return value

  const threshold = minimumNonZero > 0 ? minimumNonZero : Math.pow(10, -digits)
  const abs = Math.abs(value)
  if (abs > 0 && abs < threshold) {
    return value > 0 ? threshold : -threshold
  }
  return value
}

function formatCurrencyValue(
  value: number,
  options: ResolvedCurrencyFormatOptions,
  meta: DisplayMeta
): string {
  if (meta.kind === 'tokens') {
    if (options.compact) {
      return new Intl.NumberFormat(options.locale, {
        notation: 'compact',
        maximumFractionDigits: 1,
      }).format(value)
    }
    return formatNumberWithSuffix(
      value,
      options.digitsLarge,
      options.digitsSmall,
      options.abbreviate
    )
  }

  const digits = getFractionDigits(
    value,
    options.digitsLarge,
    options.digitsSmall
  )
  const adjustedValue = adjustForMinimum(value, digits, options.minimumNonZero)

  if (!options.showSymbol) {
    return new Intl.NumberFormat(options.locale, {
      notation: options.compact ? 'compact' : 'standard',
      minimumFractionDigits: 0,
      maximumFractionDigits: options.compact ? 1 : digits,
    }).format(adjustedValue)
  }

  return new Intl.NumberFormat(options.locale, {
    style: 'currency',
    currency: meta.currencyCode,
    currencyDisplay: 'narrowSymbol',
    notation: options.compact ? 'compact' : 'standard',
    minimumFractionDigits: 0,
    maximumFractionDigits: options.compact ? 1 : digits,
  }).format(adjustedValue)
}

/**
 * Get the current currency configuration and display metadata.
 *
 * @internal
 * This is primarily for internal use. Most consumers should use the
 * higher-level formatting functions instead.
 */
export function getCurrencyDisplay() {
  const config = getConfig()
  const meta = getDisplayMeta(config)
  return { config, meta }
}

/**
 * Format an amount in USD as it is shown: in the site currency at its rate,
 * or as raw quota in the tokens display mode.
 *
 * @example
 * // USD site
 * formatCurrencyFromUSD(10) → "$10"
 * // CNY site, usd_rate 7.2
 * formatCurrencyFromUSD(10) → "¥72"
 * // tokens display mode, quotaPerUnit 500000
 * formatCurrencyFromUSD(10) → "5000k"
 */
export function formatCurrencyFromUSD(
  amountUSD: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  if (amountUSD == null || Number.isNaN(amountUSD)) return '-'

  const { config, meta } = getCurrencyDisplay()
  const merged = mergeOptions(options)

  if (meta.kind === 'tokens') {
    const tokens = amountUSD * config.quotaPerUnit
    if (merged.compact) {
      return new Intl.NumberFormat(merged.locale, {
        notation: 'compact',
        maximumFractionDigits: 1,
      }).format(tokens)
    }
    return formatNumberWithSuffix(
      tokens,
      0,
      merged.digitsSmall,
      merged.abbreviate
    )
  }

  return formatCurrencyValue(amountUSD * meta.exchangeRate, merged, meta)
}

/**
 * Format an amount in USD as money in the site currency, even in the tokens
 * display mode. Use it for prices, where raw quota means nothing.
 */
export function formatBillingCurrencyFromUSD(
  amountUSD: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  if (amountUSD == null || Number.isNaN(amountUSD)) return '-'

  const meta = getMoneyDisplay()
  return formatCurrencyValue(
    amountUSD * meta.exchangeRate,
    mergeOptions(options),
    meta
  )
}

/**
 * Format raw quota as it is shown: converted to USD with `quotaPerUnit`,
 * then like `formatCurrencyFromUSD`.
 *
 * @example
 * // quotaPerUnit 500000, CNY site, usd_rate 7.2
 * formatQuotaWithCurrency(5000000) → "¥72"
 */
export function formatQuotaWithCurrency(
  quota: number | null | undefined,
  options?: CurrencyFormatOptions
): string {
  if (quota == null || Number.isNaN(quota)) return '-'

  const { config } = getCurrencyDisplay()
  return formatCurrencyFromUSD(quota / config.quotaPerUnit, options)
}

/**
 * The unit amounts are shown in, for labels: the code of the currency money
 * shows in ("USD", "CNY"), or "Tokens" in the tokens display mode.
 */
export function getCurrencyLabel(): string {
  const { meta } = getCurrencyDisplay()
  return meta.kind === 'tokens' ? 'Tokens' : meta.currencyCode
}

/** Whether quota shows as money rather than raw quota. */
export function isCurrencyDisplayEnabled(): boolean {
  const { meta } = getCurrencyDisplay()
  return meta.kind !== 'tokens'
}

/**
 * Format an exchange rate, a decimal string or number, with up to six
 * significant digits.
 *
 * @param rate - e.g. "7.2411296162201303"
 * @param locale - Intl locale; pass interface languages through `toIntlLocale`
 *
 * @example
 * formatExchangeRate('7.2411296162201303', 'en') → "7.24113"
 * formatExchangeRate('0.1381', 'en') → "0.1381"
 */
export function formatExchangeRate(
  rate: string | number,
  locale?: Intl.LocalesArgument
): string {
  const value = Number(rate)
  if (rate === '' || !Number.isFinite(value)) return '-'
  return new Intl.NumberFormat(locale, {
    maximumSignificantDigits: 6,
  }).format(value)
}

/**
 * Format a payment amount counted in hundredths of an ISO 4217 currency.
 *
 * Payment quotes and orders store every amount in hundredths, whatever the
 * currency's own minor unit. A whole amount keeps the currency's usual
 * decimals (none for JPY or KRW); a fractional one always shows two. An
 * unknown currency code falls back to the number followed by the code.
 *
 * @param minor - Amount in hundredths, e.g. 1050 for 10.50
 * @param currency - ISO 4217 code, e.g. "USD"
 * @param locale - Intl locale; pass interface languages through `toIntlLocale`
 *
 * @example
 * formatMinorAmount(1050, 'USD', 'en') → "$10.50"
 * formatMinorAmount(120000, 'JPY', 'ja') → "￥1,200"
 */
export function formatMinorAmount(
  minor: number,
  currency: string,
  locale?: Intl.LocalesArgument
): string {
  const value = minor / 100
  try {
    const currencyDigits =
      new Intl.NumberFormat(locale, {
        style: 'currency',
        currency,
      }).resolvedOptions().maximumFractionDigits ?? 2
    return new Intl.NumberFormat(locale, {
      style: 'currency',
      currency,
      minimumFractionDigits:
        minor % 100 === 0 ? Math.min(currencyDigits, 2) : 2,
      maximumFractionDigits: 2,
    }).format(value)
  } catch {
    const number = new Intl.NumberFormat(locale, {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(value)
    return currency ? `${number} ${currency}` : number
  }
}
