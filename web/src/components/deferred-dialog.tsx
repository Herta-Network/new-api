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
import { Suspense, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { LoadingState } from '@/components/loading-state'

// Existing Dialog owns focus, Escape and accessibility while code is loading.
// After the first open, preserve the child's mounted state and close animation.
export function DeferredDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  children: ReactNode
}) {
  const { t } = useTranslation()
  const [visited, setVisited] = useState(props.open)
  if (props.open && !visited) setVisited(true)
  if (!props.open && !visited) return null
  return (
    <Suspense
      fallback={
        props.open ? (
          <Dialog
            open
            onOpenChange={props.onOpenChange}
            title={t('Loading...')}
          >
            <LoadingState />
          </Dialog>
        ) : null
      }
    >
      {props.children}
    </Suspense>
  )
}
