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
import { getRouteApi } from '@tanstack/react-router'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import {
  useCanEditModelPricing,
  useModelPricing,
  type ServerPricingMode,
} from '@/features/model-pricing/api'
import { SERVER_PRICING_MODE_LABELS } from '@/features/model-pricing/pricing-mode'
import { MOBILE_MEDIA_QUERY, useMediaQuery } from '@/hooks/use-media-query'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { getServerMessage } from '@/lib/server-error-message'

import {
  getModelsListFilters,
  getModelsTableUrlConfig,
  modelsListQueryOptions,
  vendorsListQueryOptions,
} from '../lib/models-query'
import { DataTableBulkActions } from './data-table-bulk-actions'
import { useModelsColumns } from './models-columns'
import { useModels } from './models-provider'

const route = getRouteApi('/_authenticated/admin/models/$section')

export function ModelsTable() {
  const { t } = useTranslation()
  const { selectedVendor } = useModels()
  const canPrice = useCanEditModelPricing()
  const isMobile = useMediaQuery(MOBILE_MEDIA_QUERY)

  // URL state management
  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    ...getModelsTableUrlConfig(isMobile, canPrice),
  })
  const tableState = { globalFilter, columnFilters, pagination }

  // Fetch vendors for filter
  const { data: vendorsData } = useQuery(vendorsListQueryOptions)

  const vendors = useMemo(
    () => vendorsData?.data?.items || [],
    [vendorsData?.data?.items]
  )

  const vendorOptions = useMemo(() => {
    return vendors.map((v) => ({
      label: v.name,
      value: String(v.id),
    }))
  }, [vendors])

  const shouldSearch = getModelsListFilters(
    tableState,
    selectedVendor
  ).searching

  // Fetch models data
  const { data, isLoading, isFetching, isError, error, refetch } = useQuery(
    modelsListQueryOptions(tableState, selectedVendor)
  )

  const models = data?.data?.items || []
  const totalCount = data?.data?.total || 0
  const vendorCounts = data?.data?.vendor_counts

  // Columns configuration
  const pricingQuery = useModelPricing(
    models
      .filter((item) => item.name_rule === 0)
      .map((item) => item.model_name),
    models.length > 0
  )
  let pricingState: 'loading' | 'error' | undefined
  if (pricingQuery.isError) pricingState = 'error'
  else if (pricingQuery.isLoading) pricingState = 'loading'
  const columns = useModelsColumns(vendors, pricingQuery.data, pricingState)

  // React Table instance
  const { table } = useDataTable({
    data: models,
    getRowId: (model) =>
      model.id > 0 ? `metadata:${model.id}` : `channel:${model.model_name}`,
    columns,
    totalCount,
    initialColumnVisibility: {
      description: false,
      id: false,
      vendor_id: false,
      name_rule: false,
      endpoints: false,
      created_time: false,
      updated_time: false,
      status: false,
    },
    columnFilters,
    pagination,
    globalFilter,
    enableRowSelection: true,
    onColumnFiltersChange,
    onPaginationChange,
    onGlobalFilterChange,
    manualPagination: true,
    manualFiltering: true,
    ensurePageInRange,
  })

  // Prepare filter options
  const vendorFilterOptions = [
    {
      label: `${t('All Vendors')}${vendorCounts?.all ? ` (${vendorCounts.all})` : ''}`,
      value: 'all',
    },
    ...vendorOptions.map((option) => ({
      label: `${option.label}${vendorCounts?.[option.value] ? ` (${vendorCounts[option.value]})` : ''}`,
      value: option.value,
    })),
  ]

  if (isError || data?.success === false) {
    return (
      <ErrorState
        description={error?.message ?? getServerMessage(data)}
        onRetry={() => void refetch()}
      />
    )
  }

  return (
    <DataTablePage
      showMobileBulkActions
      mobileProps={{ enableRowSelection: true }}
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No Models Found')}
      emptyDescription={
        shouldSearch
          ? t('Try adjusting your search')
          : t('No models available. Create your first model to get started.')
      }
      skeletonKeyPrefix='model-skeleton'
      applyHeaderSize
      pinnedColumns={[
        { columnId: 'model_name', side: 'left' },
        { columnId: 'actions', side: 'right' },
      ]}
      toolbarProps={{
        searchPlaceholder: t('Filter by model name...'),
        searchDebounceMs: 500,
        filters: [
          {
            columnId: 'status',
            title: t('Display policy'),
            options: [
              { label: t('Allowed'), value: 'enabled' },
              { label: t('Not listed'), value: 'disabled' },
            ],
            singleSelect: true,
          },
          {
            columnId: 'square_state',
            title: t('Model square visibility'),
            options: [
              { label: t('Displayed'), value: 'visible' },
              { label: t('Unavailable'), value: 'unavailable' },
              { label: t('Unpriced'), value: 'unpriced' },
              { label: t('Listing hidden'), value: 'hidden' },
              { label: t('Partly shown'), value: 'partial' },
            ],
            singleSelect: true,
          },
          ...(canPrice
            ? [
                {
                  columnId: 'billing_mode',
                  title: t('Billing mode'),
                  options: (
                    Object.keys(
                      SERVER_PRICING_MODE_LABELS
                    ) as ServerPricingMode[]
                  ).map((mode) => ({
                    label: t(SERVER_PRICING_MODE_LABELS[mode]),
                    value: mode,
                  })),
                  singleSelect: true,
                },
              ]
            : []),
          {
            columnId: 'vendor_id',
            title: t('Vendor'),
            options: vendorFilterOptions,
            singleSelect: true,
          },
          {
            columnId: 'sync_official',
            title: t('Sync policy'),
            options: [
              { label: t('Allow updates'), value: 'yes' },
              { label: t('Keep local'), value: 'no' },
            ],
            singleSelect: true,
          },
        ],
      }}
      bulkActions={<DataTableBulkActions table={table} />}
    />
  )
}
