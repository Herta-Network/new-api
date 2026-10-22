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
import { zodResolver } from '@hookform/resolvers/zod'
import { Save } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Form } from '@/components/ui/form'

import { FormDirtyIndicator } from '../../components/form-dirty-indicator'
import { FormNavigationGuard } from '../../components/form-navigation-guard'
import { SettingsPageActionsPortal } from '../../components/settings-page-context'
import { useUpdateOption } from '../../hooks/use-update-option'
import { AdvancedRules } from './advanced-rules'
import { useGroupUsage } from './api'
import { GroupList } from './group-list'
import {
  buildGroupRows,
  createGroupPricingSchema,
  FIELD_SECTIONS,
  groupOptionUpdates,
  parseGroupOptions,
  toFormValues,
  type AdvancedSection,
  type GroupJsonField,
  type GroupPricingField,
  type GroupPricingValues,
} from './lib/group-options'
import { AUTO_GROUP } from './lib/group-rules'
import { PriceSimulator } from './price-simulator'

export type { GroupPricingValues } from './lib/group-options'

// The options a simulated call reads; top-up ratios do not price calls.
const SIMULATED_FIELDS: GroupJsonField[] = [
  'GroupRatio',
  'UserUsableGroups',
  'GroupGroupRatio',
  'AutoGroups',
  'GroupSpecialUsableGroup',
]

type GroupPricingSectionProps = {
  defaultValues: GroupPricingValues
  paymentCurrency: string
}

function PageBlock(props: {
  id: string
  title: string
  description: string
  children: ReactNode
}) {
  return (
    <section aria-labelledby={props.id} className='flex min-w-0 flex-col gap-4'>
      <div className='flex flex-col gap-1'>
        <h3 id={props.id} className='text-base font-semibold'>
          {props.title}
        </h3>
        <p className='text-muted-foreground text-sm'>{props.description}</p>
      </div>
      {props.children}
    </section>
  )
}

export function GroupPricingSection(props: GroupPricingSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const usage = useGroupUsage()
  const schema = useMemo(() => createGroupPricingSchema(t), [t])
  const form = useForm<GroupPricingValues>({
    resolver: zodResolver(schema),
    mode: 'onChange',
    defaultValues: toFormValues(props.defaultValues),
  })
  // What the server holds; a save sends only the fields that differ.
  const [saved, setSaved] = useState(props.defaultValues)
  const savedRef = useRef(saved)
  useEffect(() => {
    savedRef.current = saved
  }, [saved])
  const [saving, setSaving] = useState(false)
  const savingRef = useRef(false)
  const [openSections, setOpenSections] = useState<AdvancedSection[]>([])

  const values = useWatch({ control: form.control }) as GroupPricingValues
  const updates = groupOptionUpdates(saved, values)
  const isDirty = updates.length > 0

  // Settings reloaded from the server become the new baseline. The form
  // follows them unless it holds edits that are not saved yet.
  const incoming = JSON.stringify(props.defaultValues)
  const lastIncoming = useRef(incoming)
  useEffect(() => {
    if (incoming === lastIncoming.current || savingRef.current) return
    lastIncoming.current = incoming
    const next = JSON.parse(incoming) as GroupPricingValues
    const hasEdits =
      groupOptionUpdates(savedRef.current, form.getValues()).length > 0
    setSaved(next)
    if (!hasEdits) form.reset(toFormValues(next))
  }, [incoming, form])

  const parsed = useMemo(
    () =>
      parseGroupOptions({
        GroupRatio: values.GroupRatio,
        UserUsableGroups: values.UserUsableGroups,
        GroupGroupRatio: values.GroupGroupRatio,
        AutoGroups: values.AutoGroups,
        GroupSpecialUsableGroup: values.GroupSpecialUsableGroup,
        TopupGroupRatio: values.TopupGroupRatio,
      }),
    [
      values.GroupRatio,
      values.UserUsableGroups,
      values.GroupGroupRatio,
      values.AutoGroups,
      values.GroupSpecialUsableGroup,
      values.TopupGroupRatio,
    ]
  )
  const rows = useMemo(() => buildGroupRows(parsed.rules), [parsed.rules])
  const groupNames = rows.map((row) => row.name)
  const userGroups = [
    ...new Set([
      ...groupNames.filter((name) => name !== AUTO_GROUP),
      ...Object.keys(usage.data?.users ?? {}),
    ]),
  ]

  const setField = (field: GroupJsonField, value: string) => {
    form.setValue(field, value, { shouldDirty: true, shouldValidate: true })
  }

  const save = form.handleSubmit(
    async (submitted) => {
      const pending = groupOptionUpdates(saved, submitted)
      if (pending.length === 0) {
        toast.info(t('No changes to save'))
        return
      }
      savingRef.current = true
      setSaving(true)
      try {
        for (const update of pending) {
          await updateOption.mutateAsync({
            key: update.key,
            value: update.value,
          })
          setSaved((previous) => ({
            ...previous,
            [update.field]: update.value,
          }))
        }
      } catch {
        // useUpdateOption reported the failure; unsaved fields stay marked.
      } finally {
        savingRef.current = false
        setSaving(false)
      }
    },
    (errors) => {
      const fields = Object.keys(errors) as GroupPricingField[]
      const sections = fields.flatMap((field) => FIELD_SECTIONS[field] ?? [])
      setOpenSections((open) => [...new Set([...open, ...sections])])
      toast.error(t('Fix the highlighted settings before saving'))
      if (errors.MaxTokenAutoGroups) {
        requestAnimationFrame(() => form.setFocus('MaxTokenAutoGroups'))
      }
    }
  )

  return (
    <>
      <FormNavigationGuard when={isDirty && !saving} />
      <Form {...form}>
        <SettingsPageActionsPortal>
          <Button
            type='button'
            size='sm'
            onClick={() => void save()}
            disabled={saving}
          >
            <Save data-icon='inline-start' />
            {saving ? t('Saving...') : t('Save group settings')}
          </Button>
        </SettingsPageActionsPortal>
        <FormDirtyIndicator isDirty={isDirty} />

        <div className='flex min-w-0 flex-col gap-8'>
          <p className='text-muted-foreground max-w-3xl text-sm leading-6'>
            {t(
              'A group is a price tier for the users in it and a pool of channels for the tokens set to it. Every call costs the model price × the ratio of the group that serves it.'
            )}
          </p>

          <PageBlock
            id='group-pricing-groups'
            title={t('Groups')}
            description={t(
              'Add a group, set its price ratio, and choose whether users can select it for their tokens.'
            )}
          >
            <GroupList
              rules={parsed.rules}
              topupGroupRatio={parsed.topupGroupRatio}
              invalidFields={parsed.invalid}
              usage={usage.data}
              onChange={(next) => {
                setField('GroupRatio', next.GroupRatio)
                setField('UserUsableGroups', next.UserUsableGroups)
              }}
            />
          </PageBlock>

          <PageBlock
            id='group-pricing-advanced'
            title={t('Advanced rules')}
            description={t(
              'Exceptions to the list above. Most sites need none of them.'
            )}
          >
            <AdvancedRules
              rules={parsed.rules}
              topupGroupRatio={parsed.topupGroupRatio}
              groupNames={groupNames}
              invalidFields={parsed.invalid}
              paymentCurrency={props.paymentCurrency}
              open={openSections}
              onOpenChange={setOpenSections}
              onFieldChange={setField}
            />
          </PageBlock>

          <PageBlock
            id='group-pricing-simulator'
            title={t('Price simulator')}
            description={t(
              'See what one call costs for a user group, a token group and a model, step by step.'
            )}
          >
            <PriceSimulator
              rules={parsed.rules}
              userGroups={userGroups}
              blocked={SIMULATED_FIELDS.some((field) =>
                parsed.invalid.has(field)
              )}
            />
          </PageBlock>
        </div>
      </Form>
    </>
  )
}
