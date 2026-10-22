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
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatMinorAmount } from '@/lib/currency'

import { formatPricingNumber } from '../../models/pricing-format'
import { UnknownGroupBadge } from './group-name-combobox'
import { stringifyOption, type GroupJsonField } from './lib/group-options'
import { topupDue, type GroupRules, type RatioMap } from './lib/group-rules'
import { OptionEditor } from './option-editor'
import { RuleExample } from './rule-example'

type TopupRatioEditorProps = {
  rules: GroupRules
  topupGroupRatio: RatioMap
  paymentCurrency: string
  invalidFields: ReadonlySet<GroupJsonField>
  onChange: (value: string) => void
}

export function TopupRatioEditor(props: TopupRatioEditorProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const topup = props.topupGroupRatio
  const names = [
    ...Object.keys(props.rules.groupRatio),
    ...Object.keys(topup).filter(
      (name) => !Object.hasOwn(props.rules.groupRatio, name)
    ),
  ]

  const write = (name: string, ratio: number | undefined) => {
    const next = { ...topup }
    if (ratio === undefined) delete next[name]
    else next[name] = ratio
    props.onChange(stringifyOption(next))
  }

  const sample =
    names.find((name) => topup[name] !== undefined && topup[name] !== 1) ??
    names[0]
  const amount = 10_000

  return (
    <div className='flex flex-col gap-4'>
      {sample && (
        <RuleExample>
          {t(
            'A {{group}} user who tops up {{amount}} pays {{due}}, before amount discounts and payment method fees.',
            {
              group: sample,
              amount: formatMinorAmount(amount, props.paymentCurrency, locale),
              due: formatMinorAmount(
                topupDue(amount, topup[sample]),
                props.paymentCurrency,
                locale
              ),
            }
          )}
        </RuleExample>
      )}
      <p className='text-muted-foreground text-sm'>
        {t('Top-up amounts, discounts and payment methods are set in')}{' '}
        <Link
          className='text-primary underline underline-offset-4'
          to='/admin/system-settings/billing/$section'
          params={{ section: 'payment' }}
        >
          {t('Payment Settings')}
        </Link>
        .
      </p>
      <OptionEditor
        label={t('Top-up group ratios')}
        invalidFields={props.invalidFields}
        fields={[
          {
            name: 'TopupGroupRatio',
            label: t('Top-up group ratios'),
            description: t(
              'JSON object: user group → multiplier on the top-up price.'
            ),
          },
        ]}
      >
        <StaticDataTable
          tableClassName='min-w-[420px]'
          tableProps={{ 'aria-label': t('Top-up group ratios') }}
          data={names}
          getRowKey={(name) => name}
          columns={[
            {
              id: 'group',
              header: t('User group'),
              cell: (name) => (
                <span className='inline-flex items-center gap-1.5'>
                  {name}
                  {!Object.hasOwn(props.rules.groupRatio, name) && (
                    <UnknownGroupBadge />
                  )}
                </span>
              ),
            },
            {
              id: 'ratio',
              header: t('Top-up ratio'),
              className: 'w-40',
              cell: (name) => (
                <TopupRatioInput
                  label={t('Top-up ratio for {{group}}', { group: name })}
                  value={topup[name]}
                  onCommit={(ratio) => write(name, ratio)}
                />
              ),
            },
          ]}
        />
      </OptionEditor>
    </div>
  )
}

type TopupRatioInputProps = {
  label: string
  value: number | undefined
  onCommit: (value: number | undefined) => void
}

// Keeps what is typed until the field loses focus; empty means not set.
function TopupRatioInput(props: TopupRatioInputProps) {
  const { t } = useTranslation()
  const initial =
    props.value === undefined ? '' : formatPricingNumber(props.value)
  const [draft, setDraft] = useState(initial)
  const [shown, setShown] = useState(props.value)
  if (props.value !== shown) {
    setShown(props.value)
    setDraft(initial)
  }

  const commit = () => {
    const text = draft.trim()
    if (text === '') {
      if (props.value !== undefined) props.onCommit(undefined)
      return
    }
    const number = Number(text)
    if (!Number.isFinite(number) || number < 0) {
      setDraft(initial)
      return
    }
    if (number !== props.value) props.onCommit(number)
  }

  return (
    <Input
      type='number'
      inputMode='decimal'
      min={0}
      step='any'
      value={draft}
      aria-label={props.label}
      placeholder={t('Not set (×1)')}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={commit}
      onKeyDown={(event) => {
        if (event.key === 'Enter') {
          event.preventDefault()
          commit()
        }
      }}
    />
  )
}
