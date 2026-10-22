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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

// A worked example built from the settings currently on the page.
export function RuleExample(props: { children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <p className='bg-muted/50 rounded-md px-3 py-2 text-sm leading-6'>
      <span className='font-medium'>{t('Example')}: </span>
      <span className='text-muted-foreground'>{props.children}</span>
    </p>
  )
}
