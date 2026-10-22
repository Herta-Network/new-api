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
import { AlertTriangle } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import type { GroupJsonField, GroupPricingValues } from './lib/group-options'

export type OptionEditorField = {
  name: GroupJsonField
  label: string
  description: string
}

type OptionEditorProps = {
  label: string
  fields: OptionEditorField[]
  // Options that do not parse; the visual editor must not write them.
  invalidFields: ReadonlySet<GroupJsonField>
  actions?: ReactNode
  children: ReactNode
}

// One option (or a pair edited together) shown either as its visual editor or
// as raw JSON. Both edit the same form field, so switching keeps every edit.
export function OptionEditor(props: OptionEditorProps) {
  const { t } = useTranslation()
  const form = useFormContext<GroupPricingValues>()
  const [mode, setMode] = useState<'visual' | 'json'>('visual')
  const invalid = props.fields.filter((field) =>
    props.invalidFields.has(field.name)
  )

  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <ToggleGroup
          value={[mode]}
          onValueChange={(values) => {
            const next = values.find((value) => value !== mode)
            if (next === 'visual' || next === 'json') setMode(next)
          }}
          aria-label={t('Editing mode for {{name}}', { name: props.label })}
          variant='outline'
          size='sm'
        >
          <ToggleGroupItem value='visual'>{t('Visual')}</ToggleGroupItem>
          <ToggleGroupItem value='json'>JSON</ToggleGroupItem>
        </ToggleGroup>
        {mode === 'visual' && invalid.length === 0 && props.actions}
      </div>

      {mode === 'json' &&
        props.fields.map((field) => (
          <FormField
            key={field.name}
            control={form.control}
            name={field.name}
            render={({ field: control, fieldState }) => (
              <FormItem>
                <FormLabel>{field.label}</FormLabel>
                <FormControl>
                  <JsonCodeEditor
                    value={String(control.value)}
                    onChange={control.onChange}
                    name={control.name}
                    onBlur={control.onBlur}
                    textareaRef={control.ref}
                    ariaLabel={field.label}
                    aria-invalid={fieldState.invalid}
                    heightClassName='h-48 min-h-48 max-h-48'
                  />
                </FormControl>
                <FormDescription>{field.description}</FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}

      {mode === 'visual' && invalid.length > 0 && (
        <Alert variant='destructive'>
          <AlertTriangle aria-hidden='true' />
          <AlertTitle>
            {t('{{name}} is not valid JSON', {
              name: invalid.map((field) => field.label).join(', '),
            })}
          </AlertTitle>
          <AlertDescription className='flex flex-col items-start gap-3'>
            <span>
              {t(
                'The visual editor is off for it so no rule is lost. Fix it in JSON; nothing has been changed.'
              )}
            </span>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setMode('json')}
            >
              {t('Edit as JSON')}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {mode === 'visual' && invalid.length === 0 && props.children}
    </div>
  )
}
