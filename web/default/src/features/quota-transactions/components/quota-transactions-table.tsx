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
import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import {
  type SortingState,
  type VisibilityState,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { MOBILE_MEDIA_QUERY, useMediaQuery } from '@/hooks'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { useIsAdmin } from '@/hooks/use-admin'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { DataTablePage } from '@/components/data-table'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { getQuotaTransactions } from '../api'
import {
  getBucketOptions,
  getDirectionOptions,
  getTypeOptions,
} from '../constants'
import { useQuotaTransactionsColumns } from './quota-transactions-columns'

const route = getRouteApi('/_authenticated/quota-transactions/')

function getDefaultTimeRange() {
  const now = new Date()
  const start = new Date(now)
  start.setHours(0, 0, 0, 0)
  const end = new Date(now)
  end.setHours(end.getHours() + 1)
  return { start, end }
}

function toTimestamp(date?: Date) {
  if (!date) return undefined
  const time = date.getTime()
  return Number.isNaN(time) ? undefined : Math.floor(time / 1000)
}

export function QuotaTransactionsTable() {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const isMobile = useMediaQuery(MOBILE_MEDIA_QUERY)
  const columns = useQuotaTransactionsColumns(isAdmin)
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({
    direction: false,
    bucket: false,
  })
  const defaultTimeRange = useMemo(() => getDefaultTimeRange(), [])
  const [timeRange, setTimeRange] = useState<{
    start?: Date
    end?: Date
  }>(() => defaultTimeRange)
  const startTimestamp = toTimestamp(timeRange.start)
  const endTimestamp = toTimestamp(timeRange.end)

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
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    globalFilter: { enabled: isAdmin, key: 'username' },
    columnFilters: [
      { columnId: 'type', searchKey: 'type', type: 'array' },
      { columnId: 'direction', searchKey: 'direction', type: 'array' },
      { columnId: 'bucket', searchKey: 'bucket', type: 'array' },
    ],
  })

  const typeFilter =
    (columnFilters.find((f) => f.id === 'type')?.value as
      | string[]
      | undefined) ?? []
  const directionFilter =
    (columnFilters.find((f) => f.id === 'direction')?.value as
      | string[]
      | undefined) ?? []
  const bucketFilter =
    (columnFilters.find((f) => f.id === 'bucket')?.value as
      | string[]
      | undefined) ?? []

  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      'quota-transactions',
      isAdmin,
      pagination.pageIndex + 1,
      pagination.pageSize,
      globalFilter,
      typeFilter,
      directionFilter,
      bucketFilter,
      startTimestamp,
      endTimestamp,
    ],
    queryFn: async () => {
      const result = await getQuotaTransactions(
        {
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
          username: isAdmin ? globalFilter?.trim() || undefined : undefined,
          type: typeFilter.length === 1 ? typeFilter[0] : undefined,
          direction:
            directionFilter.length === 1 ? directionFilter[0] : undefined,
          bucket: bucketFilter.length === 1 ? bucketFilter[0] : undefined,
          start_timestamp: startTimestamp,
          end_timestamp: endTimestamp,
        },
        isAdmin
      )
      return {
        items: result.data?.items ?? [],
        total: result.data?.total ?? 0,
      }
    },
    placeholderData: (previousData) => previousData,
  })

  const table = useReactTable({
    data: data?.items ?? [],
    columns,
    state: {
      sorting,
      columnVisibility,
      columnFilters,
      globalFilter,
      pagination,
    },
    onSortingChange: setSorting,
    onColumnVisibilityChange: setColumnVisibility,
    onPaginationChange,
    onGlobalFilterChange,
    onColumnFiltersChange,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    manualPagination: true,
    pageCount: Math.ceil((data?.total ?? 0) / pagination.pageSize),
  })

  const pageCount = table.getPageCount()
  useEffect(() => {
    ensurePageInRange(pageCount)
  }, [pageCount, ensurePageInRange])

  const typeOptions = useMemo(() => getTypeOptions(t), [t])
  const directionOptions = useMemo(() => getDirectionOptions(t), [t])
  const bucketOptions = useMemo(() => getBucketOptions(t), [t])
  const hasCustomTimeRange =
    startTimestamp !== toTimestamp(defaultTimeRange.start) ||
    endTimestamp !== toTimestamp(defaultTimeRange.end)

  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No wallet transactions found')}
      emptyDescription={t('Quota changes will appear here.')}
      skeletonKeyPrefix='quota-transactions-skeleton'
      tableClassName={cn(
        'overflow-x-auto',
        isAdmin
          ? '[&_[data-slot=table]]:min-w-[1280px]'
          : '[&_[data-slot=table]]:min-w-[1080px]',
        '[&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:text-[13px] [&_[data-slot=table]_td_*]:text-[13px] [&_[data-slot=table]_th]:text-[13px] [&_[data-slot=table]_th_*]:text-[13px]'
      )}
      tableHeaderClassName='bg-muted/30 sticky top-0 z-10'
      applyHeaderSize
      toolbarProps={{
        customSearch: isAdmin ? undefined : null,
        additionalSearch: (
          <CompactDateTimeRangePicker
            start={timeRange.start}
            end={timeRange.end}
            onChange={setTimeRange}
            className='sm:w-[280px] lg:w-[360px]'
          />
        ),
        hasAdditionalFilters: hasCustomTimeRange,
        onReset: () => setTimeRange(defaultTimeRange),
        searchPlaceholder: t('Filter by username...'),
        filters: [
          {
            columnId: 'type',
            title: t('Type'),
            options: typeOptions,
            singleSelect: true,
          },
          {
            columnId: 'direction',
            title: t('Direction'),
            options: directionOptions,
            singleSelect: true,
          },
          {
            columnId: 'bucket',
            title: t('Quota Type'),
            options: bucketOptions,
            singleSelect: true,
          },
        ],
      }}
    />
  )
}
