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
import { Check, X } from 'lucide-react'
import { useId, useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Combobox } from '@/components/ui/combobox'
import { Field, FieldLabel } from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { usePricingData } from '@/features/pricing/hooks/use-pricing-data'
import {
  getDynamicPriceUnitLabelKey,
  getDynamicPricingSummary,
} from '@/features/pricing/lib/dynamic-price'
import { taskPriceLabel } from '@/features/pricing/lib/task-price-display'
import type { PricingModel } from '@/features/pricing/types'
import { formatBillingCurrencyFromUSD } from '@/lib/currency'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { formatPricingNumber } from '../../models/pricing-format'
import {
  AUTO_GROUP,
  resolveUsableGroups,
  simulateGroupCall,
  type GroupRules,
  type GroupSimulation,
} from './lib/group-rules'
import { modelBasePrice, withGroupRatio } from './lib/simulated-price'

const NO_GROUP = '__no_group__'

function formatUsd(value: number) {
  return formatBillingCurrencyFromUSD(value, {
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
  })
}

type PriceSimulatorProps = {
  rules: GroupRules
  userGroups: string[]
  // Set when an option the simulation reads does not parse.
  blocked: boolean
}

export function PriceSimulator(props: PriceSimulatorProps) {
  const { t } = useTranslation()
  const userGroupId = useId()
  const tokenGroupId = useId()
  const modelId = useId()
  const pricing = usePricingData()
  // Prices are formatted in the site currency; re-render when it changes.
  useSystemConfigStore((state) => state.config.currency)

  const [userGroupChoice, setUserGroup] = useState<string | null>(null)
  const [tokenGroupChoice, setTokenGroup] = useState(NO_GROUP)
  const [modelName, setModelName] = useState<string | null>(null)

  const userGroup =
    userGroupChoice && props.userGroups.includes(userGroupChoice)
      ? userGroupChoice
      : (props.userGroups.find((name) => name === 'default') ??
        props.userGroups[0] ??
        '')
  const usable = useMemo(
    () => [...resolveUsableGroups(props.rules, userGroup).keys()],
    [props.rules, userGroup]
  )
  const tokenGroup =
    tokenGroupChoice !== NO_GROUP && usable.includes(tokenGroupChoice)
      ? tokenGroupChoice
      : ''

  const models = pricing.models as PricingModel[]
  const model =
    models.find((item) => item.model_name === modelName) ?? models[0]

  const simulation = useMemo(
    () =>
      simulateGroupCall(props.rules, {
        userGroup,
        tokenGroup,
        modelGroups: model?.enable_groups,
      }),
    [props.rules, userGroup, tokenGroup, model]
  )

  const tokenGroupItems = [
    { value: NO_GROUP, label: t('No group (use the user group)') },
    ...usable.map((name) => ({
      value: name,
      label: name === AUTO_GROUP ? t('auto (auto groups)') : name,
    })),
  ]

  return (
    <div className='flex flex-col gap-5'>
      <div className='grid gap-4 md:grid-cols-3'>
        <Field>
          <FieldLabel htmlFor={userGroupId}>{t('User group')}</FieldLabel>
          <Select
            value={userGroup}
            onValueChange={(value) => value !== null && setUserGroup(value)}
            items={props.userGroups.map((name) => ({
              value: name,
              label: name,
            }))}
          >
            <SelectTrigger id={userGroupId} className='w-full'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {props.userGroups.map((name) => (
                  <SelectItem key={name} value={name}>
                    {name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel htmlFor={tokenGroupId}>{t('Token group')}</FieldLabel>
          <Select
            value={tokenGroup === '' ? NO_GROUP : tokenGroup}
            onValueChange={(value) => value !== null && setTokenGroup(value)}
            items={tokenGroupItems}
          >
            <SelectTrigger id={tokenGroupId} className='w-full'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {tokenGroupItems.map((item) => (
                  <SelectItem key={item.value} value={item.value}>
                    {item.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel htmlFor={modelId}>{t('Model')}</FieldLabel>
          <Combobox
            id={modelId}
            options={models.map((item) => ({
              value: item.model_name,
              label: item.model_name,
            }))}
            value={model?.model_name ?? null}
            onValueChange={setModelName}
            placeholder={
              pricing.isLoading ? t('Loading...') : t('Select a model')
            }
            aria-label={t('Model')}
            className='w-full'
          />
        </Field>
      </div>

      {props.blocked ? (
        <p role='status' className='text-destructive text-sm'>
          {t('Fix the options marked as not valid JSON to simulate prices.')}
        </p>
      ) : (
        <SimulationResult
          simulation={simulation}
          userGroup={userGroup}
          tokenGroup={tokenGroup}
          model={model}
        />
      )}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Uses the unsaved settings on this page with the saved model prices. Channel availability comes from the model list and may lag a minute behind.'
        )}
      </p>
    </div>
  )
}

type SimulationResultProps = {
  simulation: GroupSimulation
  userGroup: string
  tokenGroup: string
  model: PricingModel | undefined
}

function Step(props: { index: number; title: string; children: ReactNode }) {
  return (
    <li className='flex gap-3'>
      <span
        aria-hidden='true'
        className='bg-muted text-muted-foreground mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-medium'
      >
        {props.index}
      </span>
      <div className='flex min-w-0 flex-col gap-1'>
        <span className='text-sm font-medium'>{props.title}</span>
        <div className='text-muted-foreground text-sm leading-6'>
          {props.children}
        </div>
      </div>
    </li>
  )
}

function SimulationResult(props: SimulationResultProps) {
  const { t, i18n } = useTranslation()
  const { simulation, model } = props

  if (simulation.status === 'rejected') {
    return (
      <p role='status' className='text-destructive text-sm'>
        {simulation.reason === 'not_usable'
          ? t(
              'Rejected: {{user}} users cannot use the group {{group}}, so a token set to it fails.',
              { user: props.userGroup, group: simulation.tokenGroup }
            )
          : t(
              'Rejected: {{group}} has no price ratio, so tokens set to it are refused.',
              { group: simulation.tokenGroup }
            )}
      </p>
    )
  }
  if (simulation.status === 'no_auto_groups') {
    return (
      <p role='status' className='text-destructive text-sm'>
        {t(
          'Rejected: auto has no group to try for {{user}} users. Add groups they can use to the auto order.',
          { user: props.userGroup }
        )}
      </p>
    )
  }
  if (simulation.status === 'no_channel') {
    return (
      <p role='status' className='text-destructive text-sm'>
        {t(
          'No channel: none of {{groups}} has an enabled channel for {{model}}.',
          {
            groups: simulation.candidates.map((item) => item.group).join(', '),
            model: model?.model_name ?? '',
          }
        )}
      </p>
    )
  }
  if (!model) {
    return (
      <p role='status' className='text-muted-foreground text-sm'>
        {t('No model has an enabled channel yet.')}
      </p>
    )
  }

  const ratio = formatPricingNumber(simulation.ratio)
  const base = modelBasePrice(model)
  const final = withGroupRatio(base, simulation.ratio)
  const expressionBase =
    base.kind === 'expression'
      ? getDynamicPricingSummary(model, { tokenUnit: 'M' })
      : null
  const expressionFinal =
    base.kind === 'expression'
      ? getDynamicPricingSummary(model, {
          tokenUnit: 'M',
          groupRatioMultiplier: simulation.ratio,
        })
      : null
  const entryLabel = (
    entry: NonNullable<typeof expressionFinal>['primaryEntries'][number]
  ) => {
    const unitKey = getDynamicPriceUnitLabelKey(entry)
    const label =
      entry.labelKind === 'schema'
        ? taskPriceLabel(entry.description, entry.shortLabel, i18n.language)
        : t(entry.shortLabel)
    return unitKey ? `${label} / ${t(unitKey)}` : label
  }

  let routeStep: ReactNode
  if (props.tokenGroup === '') {
    routeStep = t(
      'The token has no group, so the call uses the user group {{group}}.',
      { group: simulation.billingGroup }
    )
  } else if (simulation.autoCandidates) {
    routeStep = (
      <span className='flex flex-col gap-2'>
        <span>
          {t(
            'Auto tries {{order}} and uses the first with an enabled channel for {{model}}: {{group}}.',
            {
              order: simulation.autoCandidates
                .map((item) => item.group)
                .join(' → '),
              model: model.model_name,
              group: simulation.billingGroup,
            }
          )}
        </span>
        <span className='flex flex-wrap gap-1.5'>
          {simulation.autoCandidates.map((item) => (
            <Badge
              key={item.group}
              variant={
                item.group === simulation.billingGroup ? 'default' : 'outline'
              }
            >
              {item.hasChannel === false ? (
                <X aria-hidden='true' />
              ) : (
                <Check aria-hidden='true' />
              )}
              {item.group}
              <span className='sr-only'>
                {item.hasChannel === false
                  ? t('No channel')
                  : t('Has a channel')}
              </span>
            </Badge>
          ))}
        </span>
      </span>
    )
  } else {
    routeStep = t('The token uses the group {{group}}.', {
      group: simulation.billingGroup,
    })
  }

  let ratioStep: string
  if (simulation.ratioSource === 'override') {
    ratioStep =
      simulation.groupRatio === undefined
        ? t(
            'An override for {{user}} users billed as {{group}} sets ×{{ratio}}.',
            { user: props.userGroup, group: simulation.billingGroup, ratio }
          )
        : t(
            'An override for {{user}} users billed as {{group}} sets ×{{ratio}} instead of the group ratio ×{{base}}.',
            {
              user: props.userGroup,
              group: simulation.billingGroup,
              ratio,
              base: formatPricingNumber(simulation.groupRatio),
            }
          )
  } else if (simulation.ratioSource === 'group') {
    ratioStep = t(
      'No override applies, so the ratio of {{group}} is used: ×{{ratio}}.',
      {
        group: simulation.billingGroup,
        ratio,
      }
    )
  } else {
    ratioStep = t('{{group}} has no ratio, so ×1 applies.', {
      group: simulation.billingGroup,
    })
  }

  let priceStep: ReactNode
  let finalPrice: ReactNode
  if (base.kind === 'token' && final.kind === 'token') {
    priceStep = t(
      '{{model}} lists {{input}} input and {{output}} output per 1M tokens.',
      {
        model: model.model_name,
        input: formatUsd(base.input),
        output: formatUsd(base.output),
      }
    )
    finalPrice = (
      <>
        <PriceFigure
          label={t('Input')}
          value={formatUsd(final.input)}
          unit={t('1M tokens')}
        />
        <PriceFigure
          label={t('Output')}
          value={formatUsd(final.output)}
          unit={t('1M tokens')}
        />
      </>
    )
  } else if (base.kind === 'request' && final.kind === 'request') {
    priceStep = t('{{model}} costs {{price}} per request.', {
      model: model.model_name,
      price: formatUsd(base.price),
    })
    finalPrice = (
      <PriceFigure
        label={t('Per request')}
        value={formatUsd(final.price)}
        unit={t('request')}
      />
    )
  } else if (
    expressionBase &&
    expressionFinal &&
    !expressionFinal.isSpecialExpression &&
    expressionFinal.primaryEntries.length > 0
  ) {
    priceStep = (
      <span className='flex flex-col gap-1'>
        <span>
          {expressionBase.tierCount > 1
            ? t(
                '{{model}} is priced by an expression; the first tier is shown and every tier takes the same ratio.',
                { model: model.model_name }
              )
            : t('{{model}} is priced by an expression.', {
                model: model.model_name,
              })}
        </span>
        {expressionBase.primaryEntries.map((entry) => (
          <span key={entry.key}>
            {entryLabel(entry)}: {entry.formattedRange ?? entry.formatted}
          </span>
        ))}
      </span>
    )
    finalPrice = expressionFinal.primaryEntries.map((entry) => (
      <PriceFigure
        key={entry.key}
        label={entryLabel(entry)}
        value={entry.formattedRange ?? entry.formatted}
      />
    ))
  } else {
    priceStep = t(
      '{{model}} is priced by an expression that cannot be shown as a price list. Every amount it computes is multiplied by ×{{ratio}}.',
      { model: model.model_name, ratio }
    )
    finalPrice = (
      <PriceFigure label={t('Price multiplier')} value={`×${ratio}`} />
    )
  }

  return (
    <div className='flex flex-col gap-4'>
      <div
        role='status'
        aria-label={t('Simulated price')}
        className='bg-muted/40 flex flex-wrap gap-x-8 gap-y-3 rounded-lg border p-4'
      >
        {finalPrice}
      </div>
      {simulation.hasChannel === false && (
        <p className='text-warning text-sm'>
          {t(
            '{{group}} has no enabled channel for {{model}}, so this call would fail.',
            { group: simulation.billingGroup, model: model.model_name }
          )}
        </p>
      )}
      <ol
        aria-label={t('How the price is worked out')}
        className='flex flex-col gap-3'
      >
        <Step index={1} title={t('Billing group')}>
          {routeStep}
        </Step>
        <Step index={2} title={t('Ratio')}>
          {ratioStep}
        </Step>
        <Step index={3} title={t('Model price')}>
          {priceStep}
        </Step>
        <Step index={4} title={t('Final price')}>
          {t('Model price × {{ratio}}.', { ratio })}
        </Step>
      </ol>
    </div>
  )
}

function PriceFigure(props: { label: string; value: string; unit?: string }) {
  return (
    <div className='flex min-w-0 flex-col gap-1'>
      <span className='text-muted-foreground text-xs'>{props.label}</span>
      <span className='flex flex-wrap items-baseline gap-x-1 font-mono text-lg font-semibold tabular-nums'>
        {props.value}
        {props.unit && (
          <span className='text-muted-foreground text-xs font-normal'>
            / {props.unit}
          </span>
        )}
      </span>
    </div>
  )
}
