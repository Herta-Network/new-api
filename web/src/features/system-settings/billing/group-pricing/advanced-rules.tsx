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
import type { ReactNode } from 'react'
import { useFormContext, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import {
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../../components/settings-form-layout'
import { safeNumberFieldProps } from '../../utils/numeric-field'
import { AutoGroupsEditor } from './auto-groups-editor'
import {
  sampleUserGroup,
  type AdvancedSection,
  type GroupJsonField,
  type GroupPricingValues,
} from './lib/group-options'
import {
  overrideRuleCount,
  resolveAutoGroups,
  type GroupRules,
} from './lib/group-rules'
import { OverrideRulesEditor } from './override-rules-editor'
import { RuleExample } from './rule-example'
import { SpecialUsableEditor } from './special-usable-editor'
import { TopupRatioEditor } from './topup-ratio-editor'

type AdvancedRulesProps = {
  rules: GroupRules
  topupGroupRatio: Record<string, number>
  groupNames: string[]
  invalidFields: ReadonlySet<GroupJsonField>
  paymentCurrency: string
  open: AdvancedSection[]
  onOpenChange: (open: AdvancedSection[]) => void
  onFieldChange: (field: GroupJsonField, value: string) => void
}

type SectionProps = {
  value: AdvancedSection
  title: string
  summary: string
  description: string
  children: ReactNode
}

function AdvancedRuleSection(props: SectionProps) {
  return (
    <AccordionItem value={props.value}>
      <AccordionTrigger className='gap-3 py-3 hover:no-underline'>
        <span className='flex min-w-0 flex-1 flex-col gap-1'>
          <span className='flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1'>
            <span className='font-semibold'>{props.title}</span>
            <span className='text-muted-foreground text-xs font-normal'>
              {props.summary}
            </span>
          </span>
          <span className='text-muted-foreground text-sm font-normal'>
            {props.description}
          </span>
        </span>
      </AccordionTrigger>
      <AccordionContent className='pt-1 pb-4'>
        {props.children}
      </AccordionContent>
    </AccordionItem>
  )
}

export function AdvancedRules(props: AdvancedRulesProps) {
  const { t } = useTranslation()
  const form = useFormContext<GroupPricingValues>()
  const maxGroups = useWatch({
    control: form.control,
    name: 'MaxTokenAutoGroups',
  })
  const defaultAuto = useWatch({
    control: form.control,
    name: 'DefaultUseAutoGroup',
  })

  const overrides = overrideRuleCount(props.rules)
  const specialRules = Object.values(props.rules.specialUsableGroups).reduce(
    (count, rules) => count + Object.keys(rules).length,
    0
  )
  const topupSet = Object.keys(props.topupGroupRatio).length
  const sample = sampleUserGroup(props.rules)
  const sampleOrder = sample ? resolveAutoGroups(props.rules, sample) : []
  // Examples read the parsed rules; hide them while any option is invalid.
  const examplesShown = props.invalidFields.size === 0
  const invalidSummary = t('Not valid JSON')
  const summaryOf = (field: GroupJsonField, summary: string) =>
    props.invalidFields.has(field) ? invalidSummary : summary

  return (
    <Accordion
      multiple
      value={props.open}
      onValueChange={(value) => props.onOpenChange(value as AdvancedSection[])}
      className='rounded-lg border px-4'
    >
      <AdvancedRuleSection
        value='overrides'
        title={t('Inter-group overrides')}
        summary={summaryOf(
          'GroupGroupRatio',
          overrides > 0
            ? t('Rules: {{count}}', { count: overrides })
            : t('None')
        )}
        description={t(
          'Give users of one group a different ratio when their call is billed as another group.'
        )}
      >
        <OverrideRulesEditor
          rules={props.rules}
          groupNames={props.groupNames}
          invalidFields={props.invalidFields}
          onChange={(value) => props.onFieldChange('GroupGroupRatio', value)}
        />
      </AdvancedRuleSection>

      <AdvancedRuleSection
        value='special'
        title={t('Special usable group rules')}
        summary={summaryOf(
          'GroupSpecialUsableGroup',
          specialRules > 0
            ? t('Rules: {{count}}', { count: specialRules })
            : t('None')
        )}
        description={t(
          'Let users of one group pick extra groups for their tokens, or take groups away from them.'
        )}
      >
        <SpecialUsableEditor
          rules={props.rules}
          groupNames={props.groupNames}
          invalidFields={props.invalidFields}
          onChange={(value) =>
            props.onFieldChange('GroupSpecialUsableGroup', value)
          }
        />
      </AdvancedRuleSection>

      <AdvancedRuleSection
        value='auto'
        title={t('Auto groups and their order')}
        summary={summaryOf(
          'AutoGroups',
          props.rules.autoGroups.length > 0
            ? props.rules.autoGroups.join(' → ')
            : t('Off')
        )}
        description={t(
          'Tokens set to auto try these groups in order and use the first one with a channel for the model.'
        )}
      >
        <AutoGroupsEditor
          rules={props.rules}
          invalidFields={props.invalidFields}
          onChange={(value) => props.onFieldChange('AutoGroups', value)}
        />
      </AdvancedRuleSection>

      <AdvancedRuleSection
        value='max-groups'
        title={t('Maximum custom groups per token')}
        summary={String(maxGroups)}
        description={t(
          'How many groups a token with its own auto list may use. Tokens that follow the order above are not limited.'
        )}
      >
        <div className='flex flex-col gap-4'>
          {examplesShown && Number.isInteger(maxGroups) && maxGroups > 0 && (
            <RuleExample>
              {t(
                'With {{count}}, a token that lists its own auto groups uses only the first {{count}} it may use; a token without a list tries all {{total}} groups in the order.',
                {
                  count: maxGroups,
                  total: sampleOrder.length,
                }
              )}
            </RuleExample>
          )}
          <FormField
            control={form.control}
            name='MaxTokenAutoGroups'
            render={({ field, fieldState }) => (
              <FormItem data-invalid={fieldState.invalid}>
                <FormLabel>{t('Maximum custom groups per token')}</FormLabel>
                <FormControl>
                  <Input
                    {...safeNumberFieldProps(field)}
                    type='number'
                    min={1}
                    step={1}
                    className='sm:max-w-40'
                    aria-invalid={fieldState.invalid}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Limits only token-specific Auto snapshots. Global Auto inheritance remains unlimited.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
      </AdvancedRuleSection>

      <AdvancedRuleSection
        value='default-auto'
        title={t('Default to auto groups')}
        summary={defaultAuto ? t('On') : t('Off')}
        description={t(
          "New tokens start on auto instead of the user's own group."
        )}
      >
        <div className='flex flex-col gap-4'>
          {examplesShown && (
            <RuleExample>
              {defaultAuto
                ? t(
                    "On: a new token starts with the group auto, so a {{group}} user's calls try {{order}}.",
                    {
                      group: sample ?? 'default',
                      order: sampleOrder.join(' → ') || t('None'),
                    }
                  )
                : t(
                    "Off: a new token starts with no group, so its calls are billed as the user's own group."
                  )}
            </RuleExample>
          )}
          <FormField
            control={form.control}
            name='DefaultUseAutoGroup'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Default to auto groups')}</FormLabel>
                  <FormDescription>
                    {t(
                      'If default auto group is enabled, newly created tokens start with auto instead of an empty group.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
        </div>
      </AdvancedRuleSection>

      <AdvancedRuleSection
        value='topup'
        title={t('Top-up group ratios')}
        summary={summaryOf(
          'TopupGroupRatio',
          topupSet > 0
            ? t('Set for: {{count}}', { count: topupSet })
            : t('None')
        )}
        description={t(
          'Multiply the price a user pays to top up, by user group. Calls are not affected.'
        )}
      >
        <TopupRatioEditor
          rules={props.rules}
          topupGroupRatio={props.topupGroupRatio}
          paymentCurrency={props.paymentCurrency}
          invalidFields={props.invalidFields}
          onChange={(value) => props.onFieldChange('TopupGroupRatio', value)}
        />
      </AdvancedRuleSection>
    </Accordion>
  )
}
