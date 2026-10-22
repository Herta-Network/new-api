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
import { useEffect, useMemo } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'

import { formatPricingNumber } from '../../models/pricing-format'
import type { GroupDraft, GroupRow } from './lib/group-options'
import { AUTO_GROUP } from './lib/group-rules'

export type GroupReference = { label: string; detail: string }

type GroupEditDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  // null adds a new group.
  group: GroupRow | null
  existingNames: string[]
  references: GroupReference[]
  onSubmit: (draft: GroupDraft) => void
}

function createDraftSchema(t: (key: string) => string, existing: string[]) {
  return z.object({
    name: z
      .string()
      .trim()
      .min(1, t('Enter a group name'))
      .refine(
        (name) => !name.includes(','),
        t('Group names cannot contain commas')
      )
      .refine(
        (name) => !existing.includes(name),
        t('This group already exists')
      ),
    ratio: z
      .string()
      .trim()
      .refine(
        (value) =>
          value === '' ||
          (Number.isFinite(Number(value)) && Number(value) >= 0),
        t('Enter a ratio of 0 or more')
      ),
    selectable: z.boolean(),
    description: z.string(),
  })
}

type DraftValues = z.infer<ReturnType<typeof createDraftSchema>>

export function GroupEditDrawer(props: GroupEditDrawerProps) {
  const { t } = useTranslation()
  const isNew = props.group === null
  const schema = useMemo(
    () => createDraftSchema(t, isNew ? props.existingNames : []),
    [t, isNew, props.existingNames]
  )
  const form = useForm<DraftValues>({
    resolver: zodResolver(schema),
    defaultValues: { name: '', ratio: '1', selectable: true, description: '' },
  })

  const { group, open } = props
  useEffect(() => {
    if (!open) return
    form.reset(
      group
        ? {
            name: group.name,
            ratio:
              group.ratio === undefined ? '' : formatPricingNumber(group.ratio),
            selectable: group.selectable,
            description: group.description,
          }
        : { name: '', ratio: '1', selectable: true, description: '' }
    )
  }, [open, group, form])

  const name = useWatch({ control: form.control, name: 'name' })
  const ratio = useWatch({ control: form.control, name: 'ratio' })
  const selectable = useWatch({ control: form.control, name: 'selectable' })
  const isAuto = name.trim() === AUTO_GROUP

  const submit = form.handleSubmit((values) => {
    props.onSubmit({
      name: values.name,
      ratio: values.ratio === '' ? undefined : Number(values.ratio),
      selectable: values.selectable,
      description: values.description,
    })
    props.onOpenChange(false)
  })

  return (
    <Sheet open={props.open} onOpenChange={props.onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-lg')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isNew
              ? t('Add group')
              : t('Edit group {{name}}', { name: props.group?.name })}
          </SheetTitle>
          <SheetDescription>
            {t(
              'Users in this group pay its ratio, and tokens set to it are routed through its channels.'
            )}
          </SheetDescription>
        </SheetHeader>

        <Form {...form}>
          <form
            id='group-edit-form'
            onSubmit={(event) => {
              event.stopPropagation()
              void submit(event)
            }}
            className={sideDrawerFormClassName()}
          >
            <SideDrawerSection>
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Group name')}</FormLabel>
                    <FormControl>
                      <Input {...field} disabled={!isNew} autoComplete='off' />
                    </FormControl>
                    <FormDescription>
                      {isNew
                        ? t(
                            'Use the same name you set on channels. It cannot be renamed later.'
                          )
                        : t(
                            'Users, tokens and channels refer to a group by name, so it cannot be renamed.'
                          )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='ratio'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Price ratio')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='number'
                        inputMode='decimal'
                        min={0}
                        step='any'
                        placeholder={isAuto ? t('Not set') : '1'}
                      />
                    </FormControl>
                    <FormDescription>
                      {isAuto
                        ? t(
                            'Auto has no price of its own: a call costs the ratio of the group it lands in.'
                          )
                        : t(
                            'A call billed as this group costs the model price × this ratio. 1 is the listed price, 0.8 is 20% off.'
                          )}
                    </FormDescription>
                    {!isAuto && ratio.trim() === '' && (
                      <p className='text-warning text-sm'>
                        {t(
                          'Without a ratio, tokens set to this group are rejected.'
                        )}
                      </p>
                    )}
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            <SideDrawerSection>
              <FormField
                control={form.control}
                name='selectable'
                render={({ field }) => (
                  <FormItem className={sideDrawerSwitchItemClassName()}>
                    <div className='flex flex-col gap-1'>
                      <FormLabel>{t('Users can select it')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Listed when users create a token. Otherwise only users you assign to this group, and user groups granted by a special rule, can use it.'
                        )}
                      </FormDescription>
                    </div>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='description'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Description')}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        disabled={!selectable}
                        placeholder={t('Shown next to the group name')}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Shown to users when they pick a group for a token. Only kept for groups users can select.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </SideDrawerSection>

            {!isNew && (
              <SideDrawerSection>
                <div className='flex flex-col gap-1'>
                  <h3 className='text-sm font-semibold'>
                    {t('Rules that name this group')}
                  </h3>
                  <p className='text-muted-foreground text-sm'>
                    {t('Edit them under Advanced rules.')}
                  </p>
                </div>
                {props.references.length === 0 ? (
                  <p className='text-muted-foreground text-sm'>{t('None')}</p>
                ) : (
                  <ul className='flex flex-col gap-1.5 text-sm'>
                    {props.references.map((reference) => (
                      <li
                        key={`${reference.label}:${reference.detail}`}
                        className='flex items-start justify-between gap-3 rounded-md border px-3 py-1.5'
                      >
                        <span className='text-muted-foreground'>
                          {reference.label}
                        </span>
                        <span className='text-right font-medium break-all'>
                          {reference.detail}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </SideDrawerSection>
            )}
          </form>
        </Form>

        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button type='button' variant='outline' />}>
            {t('Cancel')}
          </SheetClose>
          <Button type='submit' form='group-edit-form'>
            {isNew ? t('Add group') : t('Apply')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
