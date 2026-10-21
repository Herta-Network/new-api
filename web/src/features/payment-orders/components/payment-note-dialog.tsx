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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { PAYMENT_NOTE_MAX_LENGTH } from '../constants'

type PaymentNoteDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  desc: string
  confirmText: string
  destructive?: boolean
  isLoading?: boolean
  /** Receives the trimmed note; the dialog never confirms without one. */
  onConfirm: (note: string) => void
}

/**
 * A confirmation that records why the administrator acted. Mount it only
 * while it is open so every action starts with an empty note.
 */
export function PaymentNoteDialog(props: PaymentNoteDialogProps) {
  const { t } = useTranslation()
  const noteId = useId()
  const [note, setNote] = useState('')
  const trimmed = note.trim()

  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={props.title}
      desc={props.desc}
      confirmText={props.confirmText}
      destructive={props.destructive}
      isLoading={props.isLoading}
      disabled={trimmed === ''}
      handleConfirm={() => props.onConfirm(trimmed)}
    >
      <div className='space-y-2'>
        <Label htmlFor={noteId}>{t('Note')}</Label>
        <Textarea
          id={noteId}
          value={note}
          maxLength={PAYMENT_NOTE_MAX_LENGTH}
          rows={3}
          placeholder={t('Required, at most 500 characters')}
          onChange={(event) => setNote(event.target.value)}
        />
      </div>
    </ConfirmDialog>
  )
}
