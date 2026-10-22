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
import { Reorder } from 'motion/react'
import { useTranslation } from 'react-i18next'

import { AutoGroupOrderItem } from '@/components/auto-group-order-item'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { GroupNameCombobox, UnknownGroupBadge } from './group-name-combobox'
import {
  sampleUserGroup,
  stringifyOption,
  type GroupJsonField,
} from './lib/group-options'
import {
  AUTO_GROUP,
  resolveAutoGroups,
  type GroupRules,
} from './lib/group-rules'
import { OptionEditor } from './option-editor'
import { RuleExample } from './rule-example'

type AutoGroupsEditorProps = {
  rules: GroupRules
  invalidFields: ReadonlySet<GroupJsonField>
  onChange: (value: string) => void
}

export function AutoGroupsEditor(props: AutoGroupsEditorProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const order = props.rules.autoGroups
  const priced = Object.keys(props.rules.groupRatio)
  const candidates = priced.filter((name) => !order.includes(name))
  const write = (next: string[]) => props.onChange(stringifyOption(next))

  const move = (index: number, direction: 'up' | 'down') => {
    const target = direction === 'up' ? index - 1 : index + 1
    if (target < 0 || target >= order.length) return
    const next = [...order]
    ;[next[index], next[target]] = [next[target], next[index]]
    write(next)
  }

  const sample = sampleUserGroup(props.rules)
  let example: string | null = null
  if (sample) {
    const tried = resolveAutoGroups(props.rules, sample)
    example =
      tried.length > 0
        ? t(
            'A {{group}} user with an auto token tries {{order}}; the first group with a channel for the model serves the call.',
            { group: sample, order: tried.join(' → ') }
          )
        : t(
            'A {{group}} user with an auto token has no group to try, so the call is rejected.',
            { group: sample }
          )
  }
  const autoSelectable = Object.hasOwn(props.rules.userUsableGroups, AUTO_GROUP)
  // The gateway skips a repeated group; the list below needs unique names.
  const repeated = [
    ...new Set(order.filter((group, index) => order.indexOf(group) !== index)),
  ]

  return (
    <div className='flex flex-col gap-4'>
      {example && props.invalidFields.size === 0 && (
        <RuleExample>{example}</RuleExample>
      )}
      {!autoSelectable && (
        <p className='text-warning text-sm'>
          {t(
            'Users can only choose auto for a token when a group named auto is listed as user selectable.'
          )}
        </p>
      )}
      <OptionEditor
        label={t('Auto group order')}
        invalidFields={props.invalidFields}
        fields={[
          {
            name: 'AutoGroups',
            label: t('Auto group order'),
            description: t(
              'JSON array of group names, tried from first to last.'
            ),
          },
        ]}
      >
        <GroupNameCombobox
          label={t('Add group')}
          options={candidates}
          value={null}
          className='w-full sm:max-w-xs'
          onValueChange={(name) => {
            if (!order.includes(name)) write([...order, name])
          }}
        />
        {repeated.length > 0 && (
          <div className='flex flex-col items-start gap-2 rounded-md border p-3 text-sm'>
            <p>
              {t(
                '{{groups}} appears more than once. Only its first position is used.',
                { groups: repeated.join(', ') }
              )}
            </p>
            <Button
              type='button'
              size='sm'
              variant='outline'
              onClick={() => write([...new Set(order)])}
            >
              {t('Remove repeated entries')}
            </Button>
          </div>
        )}
        {repeated.length === 0 && order.length === 0 && (
          <EmptyState
            className='min-h-32'
            title={t('No auto groups configured')}
            description={t('Add groups in the order they should be tried.')}
          />
        )}
        {repeated.length === 0 && order.length > 0 && (
          <Reorder.Group
            as='ol'
            axis='y'
            values={order}
            onReorder={write}
            aria-label={t('Auto group order')}
            className='flex flex-col gap-2'
          >
            {order.map((group, index) => (
              <AutoGroupOrderItem
                key={group}
                group={group}
                index={index}
                count={order.length}
                onMove={move}
                onRemove={() => write(order.filter((_, i) => i !== index))}
                leading={
                  <span className='text-muted-foreground w-5 shrink-0 text-center text-sm tabular-nums'>
                    {formatNumber(index + 1, locale)}
                  </span>
                }
              >
                {!priced.includes(group) && <UnknownGroupBadge />}
              </AutoGroupOrderItem>
            ))}
          </Reorder.Group>
        )}
      </OptionEditor>
    </div>
  )
}
