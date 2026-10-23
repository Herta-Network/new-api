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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import i18next from 'i18next'
import { toast } from 'sonner'

import { invalidatePaymentSettingQueries } from '@/features/wallet/lib/wallet-queries'
import {
  siteGroupsQueryOptions,
  userGroupsQueryOptions,
} from '@/lib/group-queries'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { updatePasskeyDomains, updateSystemOption } from '../api'
import type { UpdateOptionRequest, UpdatePasskeyDomainsRequest } from '../types'

// Configuration keys that require status refresh
const STATUS_RELATED_KEYS = new Set([
  'HeaderNavModules',
  'SidebarModulesAdmin',
  'Notice',
  'LogConsumeEnabled',
  'log_setting.user_export_enabled',
  'log_setting.max_range_days',
  'QuotaPerUnit',
  'general_setting.quota_display_mode',
  'payment_setting.currency',
  'payment_setting.exchange_rate_overrides',
  'oidc.display_name',
  'ServerAddress',
  'passkey.enabled',
  'passkey.rp_id',
  'passkey.legacy_rp_ids',
  'passkey.origins',
  'QuotaRemindEnabled',
  'QuotaRemindThreshold',
])

// Options the group lists are built from. The pricing page shows the group
// ratios too.
const GROUP_LIST_KEYS = new Set([
  'GroupRatio',
  'UserUsableGroups',
  'GroupGroupRatio',
  'AutoGroups',
  'group_ratio_setting.group_special_usable_group',
])

// Options the wallet's payment and top-up settings are built from.
function isPaymentSettingKey(key: string): boolean {
  return key.startsWith('payment_setting.') || key === 'TopUpLink'
}

export function useUpdateOption() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (request: UpdateOptionRequest) =>
      requireServerSuccess(await updateSystemOption(request)),
    onSuccess: (data, variables) => {
      if (data.success) {
        // Always refresh system-options
        queryClient.invalidateQueries({ queryKey: ['system-options'] })

        if (GROUP_LIST_KEYS.has(variables.key)) {
          queryClient.invalidateQueries({
            queryKey: siteGroupsQueryOptions.queryKey,
          })
          queryClient.invalidateQueries({
            queryKey: userGroupsQueryOptions.queryKey,
          })
          queryClient.invalidateQueries({ queryKey: ['pricing'] })
        }
        if (isPaymentSettingKey(variables.key)) {
          invalidatePaymentSettingQueries(queryClient)
        }

        // If updating frontend-display-related config, also refresh status
        if (STATUS_RELATED_KEYS.has(variables.key)) {
          queryClient.invalidateQueries({ queryKey: ['status'] })
          try {
            window.localStorage.removeItem('status')
          } catch {
            /* empty */
          }
        }

        toast.success(i18next.t('Setting updated successfully'))
      } else {
        handleServerError(data, i18next.t('Failed to update setting'))
      }
    },
    onError: (error: Error) => {
      handleServerError(error, i18next.t('Failed to update setting'))
    },
  })
}

export function useUpdatePasskeyDomains() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (request: UpdatePasskeyDomainsRequest) => {
      const result = await updatePasskeyDomains(request)
      if (
        result.code === 'PASSKEY_RP_ID_REMOVAL_CONFIRMATION_REQUIRED' &&
        result.data
      ) {
        return result
      }
      return requireServerSuccess(result)
    },
    onSuccess: (result, request) => {
      if (request.preview || !result.success) return
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      queryClient.invalidateQueries({ queryKey: ['status'] })
      try {
        window.localStorage.removeItem('status')
      } catch {
        /* Storage may be disabled. */
      }
      toast.success(i18next.t('Setting updated successfully'))
    },
    onError: (error: Error) =>
      handleServerError(error, i18next.t('Failed to update setting')),
  })
}
