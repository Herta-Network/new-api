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
import { Plus } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { formatPricingNumber } from '../../models/pricing-format'
import { GroupNameCombobox, UnknownGroupBadge } from './group-name-combobox'
import { stringifyOption, type GroupJsonField } from './lib/group-options'
import { resolveGroupRatio, type GroupRules } from './lib/group-rules'
import { OptionEditor } from './option-editor'
import { RuleExample } from './rule-example'

type OverrideRule = { userGroup: string; billingGroup: string; ratio: number }

type OverrideRulesEditorProps = {
  rules: GroupRules
  groupNames: string[]
  invalidFields: ReadonlySet<GroupJsonField>
  onChange: (value: string) => void
}

export function OverrideRulesEditor(props: OverrideRulesEditorProps) {
  const { t } = useTranslation()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<OverrideRule | null>(null)

  const overrides = props.rules.groupGroupRatio
  const list: OverrideRule[] = Object.entries(overrides).flatMap(
    ([userGroup, targets]) =>
      Object.entries(targets).map(([billingGroup, ratio]) => ({
        userGroup,
        billingGroup,
        ratio,
      }))
  )
  const pricedGroups = Object.keys(props.rules.groupRatio)

  const write = (next: Record<string, Record<string, number>>) => {
    props.onChange(stringifyOption(next))
  }

  const remove = (rule: OverrideRule) => {
    const next = { ...overrides }
    const targets = { ...next[rule.userGroup] }
    delete targets[rule.billingGroup]
    if (Object.keys(targets).length === 0) delete next[rule.userGroup]
    else next[rule.userGroup] = targets
    write(next)
  }

  const save = (rule: OverrideRule) => {
    const next = { ...overrides }
    if (editing) {
      const previous = { ...next[editing.userGroup] }
      delete previous[editing.billingGroup]
      if (Object.keys(previous).length === 0) delete next[editing.userGroup]
      else next[editing.userGroup] = previous
    }
    next[rule.userGroup] = {
      ...next[rule.userGroup],
      [rule.billingGroup]: rule.ratio,
    }
    write(next)
    setDialogOpen(false)
  }

  const sample = list[0]
  const [firstGroup, secondGroup] = pricedGroups
  let example: string | null = null
  if (sample) {
    const base = props.rules.groupRatio[sample.billingGroup]
    example = t(
      'A {{user}} user whose call is billed as {{billing}} pays ×{{ratio}} instead of ×{{base}}.',
      {
        user: sample.userGroup,
        billing: sample.billingGroup,
        ratio: formatPricingNumber(sample.ratio),
        base: base === undefined ? '1' : formatPricingNumber(base),
      }
    )
  } else if (firstGroup) {
    const billing = secondGroup ?? firstGroup
    example = t(
      'With no rule, a {{user}} user whose call is billed as {{billing}} pays the ratio of {{billing}}, ×{{ratio}}.',
      {
        user: firstGroup,
        billing,
        ratio: formatPricingNumber(
          resolveGroupRatio(props.rules, firstGroup, billing).ratio
        ),
      }
    )
  }

  return (
    <div className='flex flex-col gap-4'>
      {example && <RuleExample>{example}</RuleExample>}
      <OptionEditor
        label={t('Inter-group overrides')}
        invalidFields={props.invalidFields}
        fields={[
          {
            name: 'GroupGroupRatio',
            label: t('Inter-group overrides'),
            description: t(
              'JSON object: user group → { billing group → ratio }.'
            ),
          },
        ]}
        actions={
          <Button
            type='button'
            size='sm'
            variant='outline'
            onClick={() => {
              setEditing(null)
              setDialogOpen(true)
            }}
          >
            <Plus data-icon='inline-start' />
            {t('Add override')}
          </Button>
        }
      >
        <StaticDataTable
          tableClassName='min-w-[520px]'
          tableProps={{ 'aria-label': t('Inter-group overrides') }}
          data={list}
          getRowKey={(rule) => `${rule.userGroup}\u0000${rule.billingGroup}`}
          emptyContent={
            <EmptyState
              className='min-h-32'
              title={t('No overrides')}
              description={t(
                'Every call costs the ratio of the group it is billed as.'
              )}
            />
          }
          columns={[
            {
              id: 'user-group',
              header: t('Users in'),
              cell: (rule) => (
                <span className='inline-flex items-center gap-1.5'>
                  {rule.userGroup}
                  {!props.groupNames.includes(rule.userGroup) && (
                    <UnknownGroupBadge />
                  )}
                </span>
              ),
            },
            {
              id: 'billing-group',
              header: t('Billed as'),
              cell: (rule) => (
                <span className='inline-flex items-center gap-1.5'>
                  {rule.billingGroup}
                  {!pricedGroups.includes(rule.billingGroup) && (
                    <UnknownGroupBadge />
                  )}
                </span>
              ),
            },
            {
              id: 'ratio',
              header: t('Pay'),
              cell: (rule) => {
                const base = props.rules.groupRatio[rule.billingGroup]
                return (
                  <span className='inline-flex items-baseline gap-1.5'>
                    <span className='font-mono tabular-nums'>
                      ×{formatPricingNumber(rule.ratio)}
                    </span>
                    {base !== undefined && base !== rule.ratio && (
                      <span className='text-muted-foreground text-xs'>
                        {t('(instead of {{ratio}})', {
                          ratio: formatPricingNumber(base),
                        })}
                      </span>
                    )}
                  </span>
                )
              },
            },
            {
              id: 'actions',
              header: <span className='sr-only'>{t('Actions')}</span>,
              className: 'w-24 text-right',
              cellClassName: 'text-right',
              cell: (rule) => (
                <StaticRowActions
                  editLabel={t('Edit')}
                  deleteLabel={t('Delete')}
                  menuLabel={t('Open menu')}
                  onEdit={() => {
                    setEditing(rule)
                    setDialogOpen(true)
                  }}
                  onDelete={() => remove(rule)}
                />
              ),
            },
          ]}
        />
      </OptionEditor>

      <OverrideDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        rule={editing}
        groupNames={props.groupNames}
        pricedGroups={pricedGroups}
        groupRatio={props.rules.groupRatio}
        onSave={save}
      />
    </div>
  )
}

type OverrideDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  rule: OverrideRule | null
  groupNames: string[]
  pricedGroups: string[]
  groupRatio: Record<string, number>
  onSave: (rule: OverrideRule) => void
}

function OverrideDialog(props: OverrideDialogProps) {
  const { t } = useTranslation()
  const ratioId = useId()
  const [userGroup, setUserGroup] = useState<string | null>(null)
  const [billingGroup, setBillingGroup] = useState<string | null>(null)
  const [ratio, setRatio] = useState('')
  const [submitted, setSubmitted] = useState(false)
  const [lastOpen, setLastOpen] = useState(false)

  if (props.open !== lastOpen) {
    setLastOpen(props.open)
    if (props.open) {
      setUserGroup(props.rule?.userGroup ?? null)
      setBillingGroup(props.rule?.billingGroup ?? null)
      setRatio(props.rule ? formatPricingNumber(props.rule.ratio) : '')
      setSubmitted(false)
    }
  }

  const ratioNumber = Number(ratio)
  const ratioValid =
    ratio.trim() !== '' && Number.isFinite(ratioNumber) && ratioNumber >= 0
  const base = billingGroup ? props.groupRatio[billingGroup] : undefined

  const save = () => {
    setSubmitted(true)
    if (!userGroup || !billingGroup || !ratioValid) return
    props.onSave({ userGroup, billingGroup, ratio: ratioNumber })
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.rule ? t('Edit override') : t('Add override')}
      description={t(
        'Users of one group pay a different ratio when their call is billed as another group.'
      )}
      contentHeight='auto'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button type='button' onClick={save}>
            {props.rule ? t('Apply') : t('Add')}
          </Button>
        </>
      }
    >
      <FieldGroup className='py-2'>
        <Field data-invalid={submitted && !userGroup}>
          <FieldLabel>{t('Users in')}</FieldLabel>
          <GroupNameCombobox
            label={t('Users in')}
            placeholder={t('Select a group')}
            options={props.groupNames}
            value={userGroup}
            onValueChange={setUserGroup}
          />
          <FieldDescription>
            {t('The user group the override applies to.')}
          </FieldDescription>
          {submitted && !userGroup && (
            <FieldError>{t('Select a group')}</FieldError>
          )}
        </Field>
        <Field data-invalid={submitted && !billingGroup}>
          <FieldLabel>{t('Billed as')}</FieldLabel>
          <GroupNameCombobox
            label={t('Billed as')}
            placeholder={t('Select a group')}
            options={props.pricedGroups}
            value={billingGroup}
            onValueChange={setBillingGroup}
          />
          <FieldDescription>
            {t(
              'The group whose channels serve the call: the token group, or the group auto picked.'
            )}
          </FieldDescription>
          {submitted && !billingGroup && (
            <FieldError>{t('Select a group')}</FieldError>
          )}
        </Field>
        <Field data-invalid={submitted && !ratioValid}>
          <FieldLabel htmlFor={ratioId}>{t('Ratio')}</FieldLabel>
          <Input
            id={ratioId}
            type='number'
            inputMode='decimal'
            min={0}
            step='any'
            value={ratio}
            aria-invalid={submitted && !ratioValid}
            placeholder={base === undefined ? '1' : formatPricingNumber(base)}
            onChange={(event) => setRatio(event.target.value)}
          />
          {base !== undefined && (
            <FieldDescription>
              {t('(instead of {{ratio}})', {
                ratio: formatPricingNumber(base),
              })}
            </FieldDescription>
          )}
          {submitted && !ratioValid && (
            <FieldError>{t('Enter a ratio of 0 or more')}</FieldError>
          )}
        </Field>
      </FieldGroup>
    </Dialog>
  )
}
