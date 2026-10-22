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
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { Combobox } from '@/components/ui/combobox'

type GroupNameComboboxProps = {
  options: string[]
  value: string | null
  label: string
  placeholder?: string
  onValueChange: (value: string) => void
  className?: string
  id?: string
}

// Picks a group name. A value that is not among the options (a rule naming
// a removed group) stays selectable so editing never drops it.
export function GroupNameCombobox(props: GroupNameComboboxProps) {
  const options =
    props.value && !props.options.includes(props.value)
      ? [props.value, ...props.options]
      : props.options
  return (
    <Combobox
      id={props.id}
      options={options.map((name) => ({ value: name, label: name }))}
      value={props.value}
      onValueChange={(value) => {
        if (value) props.onValueChange(value)
      }}
      className={props.className ?? 'w-full'}
      placeholder={props.placeholder ?? props.label}
      aria-label={props.label}
    />
  )
}

export function UnknownGroupBadge() {
  const { t } = useTranslation()
  return (
    <StatusBadge variant='warning' copyable={false}>
      <AlertTriangle className='size-3' aria-hidden='true' />
      {t('Not in group list')}
    </StatusBadge>
  )
}
