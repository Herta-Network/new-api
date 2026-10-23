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
import { useQuery } from '@tanstack/react-query'
import i18next from 'i18next'
import { useCallback, useMemo, useState } from 'react'
import { toast } from 'sonner'

import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { getSelf } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerMessage } from '@/lib/server-error-message'

import { transferAffiliateQuota } from '../api'
import { generateAffiliateLink } from '../lib/affiliate'
import { affiliateCodeQueryOptions } from '../lib/wallet-queries'

// ============================================================================
// Affiliate Hook
// ============================================================================

export function useAffiliate() {
  const [transferring, setTransferring] = useState(false)
  const { copyToClipboard } = useCopyToClipboard()
  const affiliateQuery = useQuery(affiliateCodeQueryOptions)
  const affiliateCode = affiliateQuery.data ?? ''
  const affiliateLink = useMemo(
    () => (affiliateCode ? generateAffiliateLink(affiliateCode) : ''),
    [affiliateCode]
  )

  // Copy affiliate link
  const copyAffiliateLink = useCallback(() => {
    copyToClipboard(affiliateLink)
  }, [affiliateLink, copyToClipboard])

  // Transfer affiliate quota to balance
  const transferQuota = useCallback(async (quota: number): Promise<boolean> => {
    try {
      setTransferring(true)
      const response = await transferAffiliateQuota({ quota })

      if (response.success) {
        toast.success(
          getServerMessage(response) || i18next.t('Transfer successful')
        )
        await getSelf()
        return true
      }

      handleServerError(response, i18next.t('Transfer failed'))
      return false
    } catch (_error) {
      handleServerError(_error, i18next.t('Transfer failed'))
      return false
    } finally {
      setTransferring(false)
    }
  }, [])

  return {
    affiliateCode,
    affiliateLink,
    loading: affiliateQuery.isLoading,
    transferring,
    copyAffiliateLink,
    transferQuota,
    refetch: affiliateQuery.refetch,
  }
}
