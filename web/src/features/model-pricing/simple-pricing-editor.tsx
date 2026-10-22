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
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { InputGroup, InputGroupAddon } from '@/components/ui/input-group'
import { Switch } from '@/components/ui/switch'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { PriceInput } from '@/features/system-settings/models/model-pricing-inputs'
import { TieredPricingEditor } from '@/features/system-settings/models/tiered-pricing-editor'

import type { PricingCurrency } from './currency'
import type { CacheWriteMode } from './pricing'
import { PricingAmountInput } from './pricing-amount-input'
import {
  buildSimplePricingExpr,
  emptySimplePricing,
  parseSimplePricingExpr,
  type SimplePriceField,
  type SimplePricing,
} from './simple-pricing'

type FieldConfig = {
  key: SimplePriceField
  label: string
  placeholder: string
}

function tokenFields(cacheWriteMode?: CacheWriteMode): FieldConfig[] {
  const claude = cacheWriteMode === 'claude_ttl'
  return [
    { key: 'input', label: 'Input price', placeholder: '3' },
    { key: 'output', label: 'Output price', placeholder: '15' },
    { key: 'cacheRead', label: 'Cache read price', placeholder: '0.3' },
    {
      key: 'cacheWrite',
      label: claude ? 'Cache write price (5 min)' : 'Cache write price',
      placeholder: '3.75',
    },
    ...(claude
      ? [
          {
            key: 'cacheWrite1h' as const,
            label: 'Cache write price (1 hour)',
            placeholder: '6',
          },
        ]
      : []),
    { key: 'imageInput', label: 'Image input price', placeholder: '2.5' },
    { key: 'audioInput', label: 'Audio input price', placeholder: '3.8' },
    { key: 'audioOutput', label: 'Audio output price', placeholder: '15' },
  ]
}

function initialDraft(billingExpr: string, requestRuleExpr: string) {
  if (requestRuleExpr.trim()) return null
  if (!billingExpr.trim()) return emptySimplePricing()
  return parseSimplePricingExpr(billingExpr)
}

/**
 * Prices a model with plain USD fields that write a canonical billing
 * expression. Expressions the fields cannot show open in the advanced editor,
 * which keeps them unchanged until edited.
 */
export function SimplePricingEditor(props: {
  currency: PricingCurrency
  modelName: string
  cacheWriteMode?: CacheWriteMode
  billingExpr: string
  requestRuleExpr: string
  onBillingExprChange: (next: string) => void
  onRequestRuleExprChange: (next: string) => void
}) {
  const { t } = useTranslation()
  const idPrefix = useId()
  const container = useRef<HTMLDivElement>(null)
  const [opened] = useState(() =>
    initialDraft(props.billingExpr, props.requestRuleExpr)
  )
  const [advanced, setAdvanced] = useState(opened === null)
  const [draft, setDraft] = useState<SimplePricing>(
    opened ?? emptySimplePricing()
  )
  const [advancedKey, setAdvancedKey] = useState(0)
  const [blocked, setBlocked] = useState('')
  const result = buildSimplePricingExpr(draft, props.cacheWriteMode)
  const issues = result.ok ? [] : result.issues
  const fields = tokenFields(props.cacheWriteMode)
  if (draft.prices.cacheWrite1h && props.cacheWriteMode !== 'claude_ttl') {
    fields.splice(4, 0, {
      key: 'cacheWrite1h',
      label: 'Cache write price (1 hour)',
      placeholder: '6',
    })
  }

  const update = (next: SimplePricing) => {
    setDraft(next)
    const built = buildSimplePricingExpr(next, props.cacheWriteMode)
    if (built.ok) props.onBillingExprChange(built.expression)
  }

  const switchAdvanced = (checked: boolean) => {
    setBlocked('')
    if (checked) {
      setAdvancedKey((key) => key + 1)
      setAdvanced(true)
      return
    }
    if (container.current?.querySelector('[data-billing-invalid="true"]')) {
      setBlocked(
        'Complete the invalid fields before saving or switching modes. The last valid expression is preserved.'
      )
      return
    }
    if (props.requestRuleExpr.trim()) {
      setBlocked(
        'Request rules need the advanced editor. Remove them to use simple prices.'
      )
      return
    }
    const parsed = initialDraft(props.billingExpr, '')
    if (!parsed) {
      setBlocked(
        'Simple prices cannot show this expression without changing what it bills. Keep editing it in the advanced editor.'
      )
      return
    }
    setDraft(parsed)
    setAdvanced(false)
  }

  const issueFor = (field: SimplePriceField | 'requestPrice') =>
    issues.find(
      (issue) => issue.field === field && issue.message !== 'Enter a price.'
    )?.message

  return (
    <div ref={container} className='flex flex-col gap-5'>
      <Field orientation='horizontal' className='rounded-lg border p-3'>
        <FieldContent>
          <FieldLabel htmlFor={`${idPrefix}-advanced`}>
            {t('Advanced editor')}
          </FieldLabel>
          <FieldDescription id={`${idPrefix}-advanced-description`}>
            {t('Tiers, conditions, time-based prices and request rules.')}
          </FieldDescription>
        </FieldContent>
        <Switch
          id={`${idPrefix}-advanced`}
          aria-describedby={`${idPrefix}-advanced-description`}
          checked={advanced}
          onCheckedChange={switchAdvanced}
        />
      </Field>
      {blocked && (
        <Alert>
          <AlertDescription role='status'>{t(blocked)}</AlertDescription>
        </Alert>
      )}

      {advanced ? (
        <TieredPricingEditor
          key={advancedKey}
          currency={props.currency}
          modelName={props.modelName}
          billingExpr={props.billingExpr}
          requestRuleExpr={props.requestRuleExpr}
          onBillingExprChange={props.onBillingExprChange}
          onRequestRuleExprChange={props.onRequestRuleExprChange}
        />
      ) : (
        <FieldGroup
          className='@container/simple-pricing gap-4'
          data-billing-invalid={!result.ok || undefined}
        >
          <ToggleGroup
            value={[draft.unit]}
            onValueChange={(values) => {
              const next = values.find((value) => value !== draft.unit)
              if (next === 'token' || next === 'request') {
                update({ ...draft, unit: next })
              }
            }}
            aria-label={t('Billing unit')}
            variant='outline'
            spacing={2}
          >
            <ToggleGroupItem value='token'>{t('Per token')}</ToggleGroupItem>
            <ToggleGroupItem value='request'>
              {t('Per request')}
            </ToggleGroupItem>
          </ToggleGroup>

          {draft.unit === 'request' ? (
            <Field data-invalid={Boolean(issueFor('requestPrice'))}>
              <FieldLabel htmlFor={`${idPrefix}-request`}>
                {t('Price per request')}
              </FieldLabel>
              <InputGroup className='has-[[data-pricing-error]]:h-auto has-[[data-pricing-error]]:flex-wrap'>
                <InputGroupAddon>{props.currency.symbol}</InputGroupAddon>
                <PricingAmountInput
                  grouped
                  id={`${idPrefix}-request`}
                  currency={props.currency}
                  value={draft.requestPrice}
                  placeholder='0.01'
                  required
                  onChange={(value) =>
                    update({ ...draft, requestPrice: value })
                  }
                />
                <InputGroupAddon align='inline-end'>
                  {t('per request')}
                </InputGroupAddon>
              </InputGroup>
              <FieldDescription>
                {t(
                  'Charged once per successful request instead of per token. Enter 0 to make it free.'
                )}
              </FieldDescription>
              {issueFor('requestPrice') && (
                <FieldError>{t(issueFor('requestPrice') ?? '')}</FieldError>
              )}
            </Field>
          ) : (
            <>
              <FieldDescription>
                {t(
                  '{{currency}} per 1M tokens. Leave an optional price empty to bill those tokens at the input or output price.',
                  { currency: props.currency.label }
                )}
              </FieldDescription>
              <div className='grid min-w-0 gap-4 @min-[560px]/simple-pricing:grid-cols-2'>
                {fields.map((field) => {
                  const issue = issueFor(field.key)
                  const required =
                    field.key === 'input' ||
                    field.key === 'output' ||
                    (field.key === 'cacheWrite1h' &&
                      props.cacheWriteMode === 'claude_ttl' &&
                      draft.prices.cacheWrite.trim() !== '')
                  return (
                    <Field
                      key={field.key}
                      className='min-w-0'
                      data-invalid={Boolean(issue)}
                    >
                      <FieldLabel htmlFor={`${idPrefix}-${field.key}`}>
                        {t(field.label)}
                        {required && <span aria-hidden='true'> *</span>}
                      </FieldLabel>
                      <PriceInput
                        id={`${idPrefix}-${field.key}`}
                        currency={props.currency}
                        value={draft.prices[field.key]}
                        placeholder={field.placeholder}
                        required={required}
                        onChange={(value) =>
                          update({
                            ...draft,
                            prices: { ...draft.prices, [field.key]: value },
                          })
                        }
                      />
                      {issue && <FieldError>{t(issue)}</FieldError>}
                    </Field>
                  )
                })}
              </div>
              {props.cacheWriteMode === 'claude_ttl' &&
                !draft.prices.cacheWrite.trim() &&
                !draft.prices.cacheWrite1h.trim() && (
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Without cache write prices, Claude cache writes that the upstream reports apart from input are free.'
                    )}
                  </p>
                )}
            </>
          )}
        </FieldGroup>
      )}
    </div>
  )
}
