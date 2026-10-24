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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { getModelCatalogStatus } from '@/features/models/api'
import { useDebounce } from '@/hooks/use-debounce'
import { requireServerSuccess } from '@/lib/server-error-message'

const PREVIEW_LIMIT = 5

/**
 * Warns while a channel is edited that users will not see some of the models
 * it supplies: the catalog shows a served model only when it is priced and
 * not hidden by its display policy.
 */
export function ModelCatalogNotice(props: { models: string[] }) {
  const { t } = useTranslation()
  const models = useDebounce(props.models, 400)
  const query = useQuery({
    queryKey: ['model-catalog-status', models],
    queryFn: async () =>
      requireServerSuccess(await getModelCatalogStatus(models)),
    enabled: models.length > 0,
    placeholderData: keepPreviousData,
    meta: { errorToast: false },
  })
  const statuses = models.length > 0 ? query.data?.data : undefined
  if (!statuses) return null
  const unpriced = models.filter((name) => statuses[name]?.priced === false)
  const hidden = models.filter((name) => statuses[name]?.listed === false)
  if (unpriced.length === 0 && hidden.length === 0) return null

  const preview = (names: string[]) => {
    const shown = names.slice(0, PREVIEW_LIMIT).join(', ')
    if (names.length <= PREVIEW_LIMIT) return shown
    return `${shown} ${t('+{{count}} more', { count: names.length - PREVIEW_LIMIT })}`
  }
  return (
    <Alert
      role='status'
      className='border-amber-200 bg-amber-50 text-amber-900 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-50'
    >
      <AlertDescription className='space-y-1'>
        <p>{t('Users will not see some models of this channel.')}</p>
        {unpriced.length > 0 && (
          <p className='break-words'>
            {t('Unpriced: {{models}}', { models: preview(unpriced) })}
          </p>
        )}
        {hidden.length > 0 && (
          <p className='break-words'>
            {t('Hidden by display policy: {{models}}', {
              models: preview(hidden),
            })}
          </p>
        )}
        <Link
          to='/admin/models/$section'
          params={{ section: 'metadata' }}
          target='_blank'
          rel='noreferrer'
          className='font-medium underline underline-offset-4'
        >
          {t('Open model management')}
        </Link>
      </AlertDescription>
    </Alert>
  )
}
