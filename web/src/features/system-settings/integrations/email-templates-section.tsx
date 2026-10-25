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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useDebounce } from '@/hooks/use-debounce'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import {
  EMAIL_KIND_LABELS,
  EMAIL_LANGUAGE_LABELS,
  EMAIL_LAYOUT_KIND,
  useEmailTemplatePreview,
  useEmailTemplates,
  useResetEmailTemplate,
  useSaveEmailTemplate,
  type EmailTemplate,
} from './email-api'

/**
 * Edits the template of each email in each backend language, with a live
 * preview rendered by the server from sample values.
 */
export function EmailTemplatesSection() {
  const { t } = useTranslation()
  const templates = useEmailTemplates()
  const saveTemplate = useSaveEmailTemplate()
  const resetTemplate = useResetEmailTemplate()
  const [selectedKind, setSelectedKind] = useState<string | null>(null)
  const [selectedLanguage, setSelectedLanguage] = useState<string | null>(null)
  // Unsaved edits, kept per email and language while switching between them.
  const [drafts, setDrafts] = useState<Record<string, EmailTemplate>>({})
  const [resetOpen, setResetOpen] = useState(false)

  const info = templates.data
  const kind =
    info?.templates.find((item) => item.kind === selectedKind) ??
    info?.templates[0]
  const language = selectedLanguage ?? info?.default_language ?? 'en'
  const entry = kind?.languages.find((item) => item.language === language)
  const draftKey = `${kind?.kind}:${language}`
  const saved = entry?.template
  const template = drafts[draftKey] ?? saved
  const dirty =
    template !== undefined &&
    saved !== undefined &&
    (template.subject !== saved.subject || template.body !== saved.body)
  const isLayout = kind?.kind === EMAIL_LAYOUT_KIND
  const previewTemplate = useDebounce(template, 500)
  const preview = useEmailTemplatePreview(
    kind && previewTemplate
      ? { kind: kind.kind, language, template: previewTemplate }
      : null
  )

  if (templates.isLoading) {
    return (
      <SettingsSection title={t('Email Templates')}>
        <LoadingState />
      </SettingsSection>
    )
  }
  if (!info || !kind || !entry || !template) {
    return (
      <SettingsSection title={t('Email Templates')}>
        <ErrorState
          description={getServerErrorMessage(templates.error)}
          onRetry={() => void templates.refetch()}
        />
      </SettingsSection>
    )
  }

  const editDraft = (change: Partial<EmailTemplate>) =>
    setDrafts((current) => ({
      ...current,
      [draftKey]: { ...template, ...change },
    }))
  const discardDraft = () =>
    setDrafts((current) => {
      const next = { ...current }
      delete next[draftKey]
      return next
    })
  const target = { kind: kind.kind, language }

  return (
    <SettingsSection title={t('Email Templates')}>
      <SettingsPageFormActions
        onSave={() =>
          saveTemplate.mutate(
            { ...target, template },
            { onSuccess: discardDraft }
          )
        }
        isSaving={saveTemplate.isPending}
        isSaveDisabled={!dirty}
        onReset={() => setResetOpen(true)}
        resetLabel='Restore built-in template'
        isResetDisabled={!entry.customized || resetTemplate.isPending}
      />
      <p className='text-muted-foreground text-sm'>
        {t(
          'Each email has a built-in template in every language. A replaced template is used for readers of its language; other languages keep theirs.'
        )}
      </p>

      <div className='grid gap-4 sm:grid-cols-2'>
        <div className='flex min-w-0 flex-col gap-2'>
          <Label htmlFor='email-template-kind'>{t('Email')}</Label>
          <Select
            items={info.templates.map((item) => ({
              value: item.kind,
              label: t(EMAIL_KIND_LABELS[item.kind] ?? item.kind),
            }))}
            value={kind.kind}
            onValueChange={(value) => setSelectedKind(value as string)}
          >
            <SelectTrigger id='email-template-kind' className='w-full'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {info.templates.map((item) => (
                  <SelectItem key={item.kind} value={item.kind}>
                    {t(EMAIL_KIND_LABELS[item.kind] ?? item.kind)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
        <div className='flex min-w-0 flex-col gap-2'>
          <Label htmlFor='email-template-language'>{t('Email language')}</Label>
          <Select
            items={info.languages.map((item) => ({
              value: item,
              label: t(EMAIL_LANGUAGE_LABELS[item] ?? item),
            }))}
            value={language}
            onValueChange={(value) => setSelectedLanguage(value as string)}
          >
            <SelectTrigger id='email-template-language' className='w-full'>
              <SelectValue />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {info.languages.map((item) => (
                  <SelectItem key={item} value={item}>
                    {t(EMAIL_LANGUAGE_LABELS[item] ?? item)}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className='flex flex-wrap items-center gap-2'>
        <Badge variant={entry.customized ? 'default' : 'outline'}>
          {entry.customized ? t('Customized') : t('Built-in')}
        </Badge>
        {dirty && (
          <>
            <Badge variant='warning'>{t('Unsaved changes')}</Badge>
            <Button
              type='button'
              variant='ghost'
              size='sm'
              onClick={discardDraft}
            >
              {t('Discard changes')}
            </Button>
          </>
        )}
      </div>

      <div className='flex flex-col gap-2'>
        <span className='text-sm font-medium'>{t('Variables')}</span>
        <div className='flex flex-wrap gap-2'>
          {['SystemName', ...kind.variables].map((name) => {
            const variable = `{{.${name}}}`
            return (
              <span
                key={name}
                className='bg-muted inline-flex items-center gap-1 rounded-md py-0.5 pr-0.5 pl-2 font-mono text-xs'
              >
                {variable}
                {kind.required.includes(name) && (
                  <span className='text-muted-foreground font-sans'>
                    {t('(required)')}
                  </span>
                )}
                <CopyButton
                  value={variable}
                  size='sm'
                  className='size-6'
                  aria-label={t('Copy {{field}}', { field: variable })}
                />
              </span>
            )
          })}
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Templates use Go template syntax, limited to variables and if blocks. Values are escaped, so they cannot add markup.'
          )}
        </p>
      </div>

      <div className='grid min-w-0 gap-6 lg:grid-cols-2'>
        <div className='flex min-w-0 flex-col gap-4'>
          {!isLayout && (
            <div className='flex flex-col gap-2'>
              <Label htmlFor='email-template-subject'>{t('Subject')}</Label>
              <Input
                id='email-template-subject'
                value={template.subject}
                onChange={(event) => editDraft({ subject: event.target.value })}
              />
            </div>
          )}
          <div className='flex flex-col gap-2'>
            <Label htmlFor='email-template-body'>{t('HTML body')}</Label>
            <Textarea
              id='email-template-body'
              value={template.body}
              onChange={(event) => editDraft({ body: event.target.value })}
              spellCheck={false}
              className='min-h-96 font-mono text-xs md:text-xs'
            />
          </div>
        </div>
        <div className='flex min-w-0 flex-col gap-2'>
          <span className='text-sm font-medium'>{t('Preview')}</span>
          {preview.isError ? (
            <p role='alert' className='text-destructive text-sm'>
              {getServerErrorMessage(preview.error)}
            </p>
          ) : (
            <>
              {!isLayout && (
                <p className='truncate text-sm'>
                  <span className='text-muted-foreground'>
                    {t('Subject')}:{' '}
                  </span>
                  {preview.data?.subject}
                </p>
              )}
              <iframe
                title={t('Email preview')}
                // No scripts, forms or same-origin access for the preview.
                sandbox=''
                srcDoc={preview.data?.html ?? ''}
                className='h-[32rem] w-full rounded-lg border bg-white'
              />
            </>
          )}
          <p className='text-muted-foreground text-xs'>
            {isLayout
              ? t('The layout is previewed around the verification email.')
              : t('Rendered with sample values.')}
          </p>
        </div>
      </div>

      <ConfirmDialog
        open={resetOpen}
        onOpenChange={setResetOpen}
        title={t('Restore built-in template')}
        desc={t(
          'The saved template of this email in this language is deleted, and the built-in template is used again.'
        )}
        confirmText={t('Restore')}
        destructive
        isLoading={resetTemplate.isPending}
        handleConfirm={() =>
          resetTemplate.mutate(target, {
            onSuccess: () => {
              discardDraft()
              setResetOpen(false)
            },
          })
        }
      />
    </SettingsSection>
  )
}
