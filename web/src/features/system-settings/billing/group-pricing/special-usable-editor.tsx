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
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import { GroupNameCombobox, UnknownGroupBadge } from './group-name-combobox'
import { stringifyOption, type GroupJsonField } from './lib/group-options'
import {
  listSpecialUsableRules,
  resolveUsableGroups,
  type GroupRules,
  type SpecialUsableRule,
} from './lib/group-rules'
import { OptionEditor } from './option-editor'
import { RuleExample } from './rule-example'

type SpecialUsableEditorProps = {
  rules: GroupRules
  groupNames: string[]
  invalidFields: ReadonlySet<GroupJsonField>
  onChange: (value: string) => void
}

type RuleDraft = {
  userGroup: string
  action: 'add' | 'remove'
  group: string
  description: string
}

export function SpecialUsableEditor(props: SpecialUsableEditorProps) {
  const { t } = useTranslation()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<SpecialUsableRule | null>(null)

  const map = props.rules.specialUsableGroups
  const list = listSpecialUsableRules(map)

  const withoutRule = (rule: { userGroup: string; key: string }) => {
    const next = { ...map }
    const inner = { ...next[rule.userGroup] }
    delete inner[rule.key]
    if (Object.keys(inner).length === 0) delete next[rule.userGroup]
    else next[rule.userGroup] = inner
    return next
  }

  // Keys this editor writes always carry the prefix; the gateway reads a key
  // without one the same way as "+:".
  const save = (draft: RuleDraft) => {
    const next = editing ? withoutRule(editing) : { ...map }
    const key = `${draft.action === 'add' ? '+' : '-'}:${draft.group}`
    next[draft.userGroup] = {
      ...next[draft.userGroup],
      [key]: draft.action === 'add' ? draft.description : 'remove',
    }
    props.onChange(stringifyOption(next))
    setDialogOpen(false)
  }

  const sampleGroup =
    list[0]?.userGroup ?? Object.keys(props.rules.groupRatio)[0]
  let example: string | null = null
  if (sampleGroup) {
    const usable = [...resolveUsableGroups(props.rules, sampleGroup).keys()]
    const own = list.filter((rule) => rule.userGroup === sampleGroup)
    const added = own.filter((rule) => rule.action === 'add')
    const removed = own.filter((rule) => rule.action === 'remove')
    example = t('Users in {{group}} can pick {{groups}} for a token.', {
      group: sampleGroup,
      groups: usable.join(', ') || t('None'),
    })
    const addedGroups = added.map((rule) => rule.group).join(', ')
    const removedGroups = removed.map((rule) => rule.group).join(', ')
    if (added.length > 0 && removed.length > 0) {
      example += ` ${t('Their rules add {{added}} and remove {{removed}}.', {
        added: addedGroups,
        removed: removedGroups,
      })}`
    } else if (added.length > 0) {
      example += ` ${t('Their rules add {{added}}.', { added: addedGroups })}`
    } else if (removed.length > 0) {
      example += ` ${t('Their rules remove {{removed}}.', {
        removed: removedGroups,
      })}`
    }
  }

  return (
    <div className='flex flex-col gap-4'>
      {example && props.invalidFields.size === 0 && (
        <RuleExample>{example}</RuleExample>
      )}
      <OptionEditor
        label={t('Special usable group rules')}
        invalidFields={props.invalidFields}
        fields={[
          {
            name: 'GroupSpecialUsableGroup',
            label: t('Special usable group rules'),
            description: t(
              'JSON object: user group → { "+:group" or "group" adds it, "-:group" removes it }.'
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
            {t('Add rule')}
          </Button>
        }
      >
        <StaticDataTable
          tableClassName='min-w-[560px]'
          tableProps={{ 'aria-label': t('Special usable group rules') }}
          data={list}
          getRowKey={(rule) => `${rule.userGroup}\u0000${rule.key}`}
          emptyContent={
            <EmptyState
              className='min-h-32'
              title={t('No special rules')}
              description={t(
                'Every user group sees the selectable groups, plus its own group.'
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
              id: 'effect',
              header: t('Effect'),
              cell: (rule) => (
                <StatusBadge
                  label={
                    rule.action === 'add'
                      ? t('Can also pick')
                      : t('Cannot pick')
                  }
                  variant={rule.action === 'add' ? 'info' : 'danger'}
                  copyable={false}
                />
              ),
            },
            {
              id: 'group',
              header: t('Group'),
              cell: (rule) => (
                <span className='inline-flex items-center gap-1.5'>
                  {rule.group}
                  {!props.groupNames.includes(rule.group) && (
                    <UnknownGroupBadge />
                  )}
                </span>
              ),
            },
            {
              id: 'description',
              header: t('Description'),
              cell: (rule) =>
                rule.action === 'add' && rule.description ? (
                  rule.description
                ) : (
                  <span className='text-muted-foreground'>—</span>
                ),
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
                  onDelete={() =>
                    props.onChange(stringifyOption(withoutRule(rule)))
                  }
                />
              ),
            },
          ]}
        />
      </OptionEditor>

      <SpecialRuleDialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        rule={editing}
        groupNames={props.groupNames}
        onSave={save}
      />
    </div>
  )
}

type SpecialRuleDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  rule: SpecialUsableRule | null
  groupNames: string[]
  onSave: (draft: RuleDraft) => void
}

function SpecialRuleDialog(props: SpecialRuleDialogProps) {
  const { t } = useTranslation()
  const descriptionId = useId()
  const [draft, setDraft] = useState<RuleDraft>({
    userGroup: '',
    action: 'add',
    group: '',
    description: '',
  })
  const [submitted, setSubmitted] = useState(false)
  const [lastOpen, setLastOpen] = useState(false)

  if (props.open !== lastOpen) {
    setLastOpen(props.open)
    if (props.open) {
      setDraft({
        userGroup: props.rule?.userGroup ?? '',
        action: props.rule?.action ?? 'add',
        group: props.rule?.group ?? '',
        description: props.rule?.action === 'add' ? props.rule.description : '',
      })
      setSubmitted(false)
    }
  }

  const save = () => {
    setSubmitted(true)
    if (!draft.userGroup || !draft.group) return
    props.onSave(draft)
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.rule ? t('Edit rule') : t('Add rule')}
      description={t(
        'Change which groups users of one group can pick for their tokens.'
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
        <Field data-invalid={submitted && !draft.userGroup}>
          <FieldLabel>{t('Users in')}</FieldLabel>
          <GroupNameCombobox
            label={t('Users in')}
            placeholder={t('Select a group')}
            options={props.groupNames}
            value={draft.userGroup || null}
            onValueChange={(userGroup) => setDraft({ ...draft, userGroup })}
          />
          {submitted && !draft.userGroup && (
            <FieldError>{t('Select a group')}</FieldError>
          )}
        </Field>
        <Field>
          <FieldLabel>{t('Effect')}</FieldLabel>
          <ToggleGroup
            value={[draft.action]}
            onValueChange={(values) => {
              const next = values.find((value) => value !== draft.action)
              if (next === 'add' || next === 'remove') {
                setDraft({ ...draft, action: next })
              }
            }}
            aria-label={t('Effect')}
            variant='outline'
            spacing={2}
          >
            <ToggleGroupItem value='add'>{t('Can also pick')}</ToggleGroupItem>
            <ToggleGroupItem value='remove'>{t('Cannot pick')}</ToggleGroupItem>
          </ToggleGroup>
        </Field>
        <Field data-invalid={submitted && !draft.group}>
          <FieldLabel>{t('Group')}</FieldLabel>
          <GroupNameCombobox
            label={t('Group')}
            placeholder={t('Select a group')}
            options={props.groupNames}
            value={draft.group || null}
            onValueChange={(group) => setDraft({ ...draft, group })}
          />
          {submitted && !draft.group && (
            <FieldError>{t('Select a group')}</FieldError>
          )}
        </Field>
        {draft.action === 'add' && (
          <Field>
            <FieldLabel htmlFor={descriptionId}>{t('Description')}</FieldLabel>
            <Input
              id={descriptionId}
              value={draft.description}
              onChange={(event) =>
                setDraft({ ...draft, description: event.target.value })
              }
            />
            <FieldDescription>
              {t('Shown to these users when they pick a group for a token.')}
            </FieldDescription>
          </Field>
        )}
      </FieldGroup>
    </Dialog>
  )
}
