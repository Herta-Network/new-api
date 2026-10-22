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
import { AlertTriangle, Plus, Search, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { EmptyState } from '@/components/empty-state'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatPricingNumber } from '../../models/pricing-format'
import type { GroupUsage } from './api'
import { GroupEditDrawer, type GroupReference } from './group-edit-drawer'
import {
  buildGroupRows,
  groupAccess,
  removeGroup,
  writeGroup,
  type GroupAccess,
  type GroupDraft,
  type GroupJsonField,
  type GroupRow,
} from './lib/group-options'
import {
  AUTO_GROUP,
  listSpecialUsableRules,
  type GroupRules,
  type SpecialUsableRule,
} from './lib/group-rules'
import { OptionEditor } from './option-editor'

type GroupListProps = {
  rules: GroupRules
  topupGroupRatio: Record<string, number>
  invalidFields: ReadonlySet<GroupJsonField>
  usage: GroupUsage | undefined
  onChange: (values: { GroupRatio: string; UserUsableGroups: string }) => void
}

const ACCESS_LABELS: Record<GroupAccess, string> = {
  selectable: 'User selectable',
  assigned: 'Admin assigned',
  hidden: 'Hidden from everyone',
}

const ACCESS_VARIANTS = {
  selectable: 'info',
  assigned: 'neutral',
  hidden: 'warning',
} as const

function specialRulesFor(rules: SpecialUsableRule[], group: string) {
  const forGroup = rules.filter((rule) => rule.group === group)
  return {
    grantedTo: forGroup
      .filter((rule) => rule.action === 'add')
      .map((rule) => rule.userGroup),
    hiddenFrom: forGroup
      .filter((rule) => rule.action === 'remove')
      .map((rule) => rule.userGroup),
  }
}

export function GroupList(props: GroupListProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState<GroupRow | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [removing, setRemoving] = useState<GroupRow | null>(null)

  const rows = useMemo(() => buildGroupRows(props.rules), [props.rules])
  const specialRules = useMemo(
    () => listSpecialUsableRules(props.rules.specialUsableGroups),
    [props.rules.specialUsableGroups]
  )

  const referencesFor = (name: string): GroupReference[] => {
    const references: GroupReference[] = []
    const overrides = props.rules.groupGroupRatio
    for (const [billingGroup, ratio] of Object.entries(overrides[name] ?? {})) {
      references.push({
        label: t('Override for its users'),
        detail: t('{{group}} ×{{ratio}}', {
          group: billingGroup,
          ratio: formatPricingNumber(ratio),
        }),
      })
    }
    for (const [userGroup, targets] of Object.entries(overrides)) {
      if (!Object.hasOwn(targets, name)) continue
      references.push({
        label: t('Override when billed as it'),
        detail: t('{{group}} users ×{{ratio}}', {
          group: userGroup,
          ratio: formatPricingNumber(targets[name]),
        }),
      })
    }
    for (const rule of specialRules) {
      if (rule.group === name) {
        references.push({
          label:
            rule.action === 'add'
              ? t('Granted by a special rule')
              : t('Hidden by a special rule'),
          detail: rule.userGroup,
        })
      } else if (rule.userGroup === name) {
        references.push({
          label:
            rule.action === 'add'
              ? t('Its users also get')
              : t('Its users lose'),
          detail: rule.group,
        })
      }
    }
    const autoIndex = props.rules.autoGroups.indexOf(name)
    if (autoIndex >= 0) {
      references.push({
        label: t('Auto group order'),
        detail: t('Position {{position}}', { position: autoIndex + 1 }),
      })
    }
    if (Object.hasOwn(props.topupGroupRatio, name)) {
      references.push({
        label: t('Top-up ratio'),
        detail: `×${formatPricingNumber(props.topupGroupRatio[name])}`,
      })
    }
    // "+:x" and "x" in one rule set read the same; list them once.
    return references.filter(
      (reference, index) =>
        references.findIndex(
          (other) =>
            other.label === reference.label && other.detail === reference.detail
        ) === index
    )
  }

  const query = search.trim().toLowerCase()
  const visibleRows = rows.filter(
    (row) =>
      !query ||
      row.name.toLowerCase().includes(query) ||
      row.description.toLowerCase().includes(query)
  )

  const applyDraft = (draft: GroupDraft) => {
    props.onChange(writeGroup(props.rules, draft))
  }

  const confirmRemove = () => {
    if (!removing) return
    props.onChange(removeGroup(props.rules, removing.name))
    setRemoving(null)
  }

  const removingReferences = removing ? referencesFor(removing.name) : []

  return (
    <OptionEditor
      label={t('Groups')}
      invalidFields={props.invalidFields}
      fields={[
        {
          name: 'GroupRatio',
          label: t('Group ratios'),
          description: t('JSON object: group → price ratio.'),
        },
        {
          name: 'UserUsableGroups',
          label: t('Selectable groups'),
          description: t(
            'JSON object: group → description, for the groups users can select when creating a token.'
          ),
        },
      ]}
      actions={
        <Button
          type='button'
          size='sm'
          onClick={() => {
            setSearch('')
            setEditing(null)
            setDrawerOpen(true)
          }}
        >
          <Plus data-icon='inline-start' />
          {t('Add group')}
        </Button>
      }
    >
      <InputGroup className='w-full sm:max-w-sm'>
        <InputGroupAddon>
          <Search aria-hidden='true' />
        </InputGroupAddon>
        <InputGroupInput
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          aria-label={t('Search groups by name or description')}
          placeholder={t('Search groups by name or description')}
        />
        {search && (
          <InputGroupAddon align='inline-end'>
            <InputGroupButton
              size='icon-xs'
              aria-label={t('Clear search')}
              onClick={() => setSearch('')}
            >
              <X aria-hidden='true' />
            </InputGroupButton>
          </InputGroupAddon>
        )}
      </InputGroup>

      <StaticDataTable
        tableClassName='min-w-[760px]'
        tableProps={{ 'aria-label': t('Groups') }}
        data={visibleRows}
        getRowKey={(row) => row.name}
        emptyClassName='text-muted-foreground h-20 text-sm'
        emptyContent={
          <EmptyState
            className='min-h-40'
            title={
              query
                ? t('No results found')
                : t('No groups yet. Add a group to get started.')
            }
            action={
              query ? (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  onClick={() => setSearch('')}
                >
                  {t('Clear search')}
                </Button>
              ) : undefined
            }
          />
        }
        columns={[
          {
            id: 'group',
            header: t('Group'),
            className: 'min-w-36',
            cell: (row) => (
              <span className='inline-flex max-w-56 items-center gap-1.5'>
                <span className='truncate font-medium' title={row.name}>
                  {row.name}
                </span>
                {row.name === AUTO_GROUP && (
                  <StatusBadge
                    label={t('Auto routing')}
                    variant='purple'
                    copyable={false}
                  />
                )}
              </span>
            ),
          },
          {
            id: 'ratio',
            header: t('Price ratio'),
            className: 'w-32',
            cell: (row) => {
              if (row.ratio !== undefined) {
                return (
                  <span className='font-mono tabular-nums'>
                    ×{formatPricingNumber(row.ratio)}
                  </span>
                )
              }
              if (row.name === AUTO_GROUP) {
                return (
                  <span className='text-muted-foreground text-sm'>
                    {t('Group it lands in')}
                  </span>
                )
              }
              return (
                <span className='text-warning inline-flex items-center gap-1 text-sm'>
                  <AlertTriangle className='size-3.5' aria-hidden='true' />
                  {t('No ratio')}
                </span>
              )
            },
          },
          {
            id: 'access',
            header: t('Who can use it'),
            className: 'min-w-44',
            cell: (row) => {
              const special = specialRulesFor(specialRules, row.name)
              const access = groupAccess(
                row,
                special.grantedTo,
                props.usage ? (props.usage.users[row.name] ?? 0) : undefined
              )
              return (
                <div className='flex flex-col items-start gap-1'>
                  <StatusBadge
                    label={t(ACCESS_LABELS[access])}
                    variant={ACCESS_VARIANTS[access]}
                    copyable={false}
                  />
                  {row.selectable && special.hiddenFrom.length > 0 && (
                    <span className='text-muted-foreground text-xs'>
                      {t('Hidden from {{groups}}', {
                        groups: special.hiddenFrom.join(', '),
                      })}
                    </span>
                  )}
                  {!row.selectable && special.grantedTo.length > 0 && (
                    <span className='text-muted-foreground text-xs'>
                      {t('Also for {{groups}} users', {
                        groups: special.grantedTo.join(', '),
                      })}
                    </span>
                  )}
                </div>
              )
            },
          },
          {
            id: 'description',
            header: t('Description'),
            className: 'min-w-40',
            cell: (row) =>
              row.selectable && row.description ? (
                row.description
              ) : (
                <span className='text-muted-foreground'>—</span>
              ),
          },
          {
            id: 'users',
            header: t('Users'),
            className: 'w-20 text-right',
            cellClassName: 'text-right tabular-nums',
            cell: (row) =>
              props.usage
                ? formatNumber(props.usage.users[row.name] ?? 0, locale)
                : '—',
          },
          {
            id: 'tokens',
            header: t('Tokens'),
            className: 'w-20 text-right',
            cellClassName: 'text-right tabular-nums',
            cell: (row) =>
              props.usage
                ? formatNumber(props.usage.tokens[row.name] ?? 0, locale)
                : '—',
          },
          {
            id: 'actions',
            header: <span className='sr-only'>{t('Actions')}</span>,
            className: 'w-24 text-right',
            cellClassName: 'text-right',
            cell: (row) => (
              <StaticRowActions
                editLabel={t('Edit group {{name}}', { name: row.name })}
                deleteLabel={t('Delete')}
                menuLabel={t('Actions for {{name}}', { name: row.name })}
                onEdit={() => {
                  setEditing(row)
                  setDrawerOpen(true)
                }}
                onDelete={() => setRemoving(row)}
              />
            ),
          },
        ]}
      />

      <GroupEditDrawer
        open={drawerOpen}
        onOpenChange={setDrawerOpen}
        group={editing}
        existingNames={rows.map((row) => row.name)}
        references={editing ? referencesFor(editing.name) : []}
        onSubmit={applyDraft}
      />

      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) setRemoving(null)
        }}
        title={t('Remove group {{name}}?', { name: removing?.name })}
        desc={
          <div className='flex flex-col gap-2'>
            {props.usage && removing && (
              <p>
                {t(
                  '{{users}} users and {{tokens}} tokens use it. Calls they make as this group fall back to ratio 1, and tokens set to it are rejected.',
                  {
                    users: formatNumber(
                      props.usage.users[removing.name] ?? 0,
                      locale
                    ),
                    tokens: formatNumber(
                      props.usage.tokens[removing.name] ?? 0,
                      locale
                    ),
                  }
                )}
              </p>
            )}
            <p>
              {removingReferences.length > 0
                ? t(
                    'Rules that name it are kept until you change them: {{rules}}.',
                    {
                      rules: removingReferences
                        .map(
                          (reference) =>
                            `${reference.label} (${reference.detail})`
                        )
                        .join('; '),
                    }
                  )
                : t('No other rule names it.')}
            </p>
            <p>{t('Nothing is saved until you save group settings.')}</p>
          </div>
        }
        destructive
        confirmText={t('Remove')}
        handleConfirm={confirmRemove}
      />
    </OptionEditor>
  )
}
