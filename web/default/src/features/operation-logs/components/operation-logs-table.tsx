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
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
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
import { useTranslation } from 'react-i18next'
import { MOBILE_MEDIA_QUERY, useMediaQuery } from '@/hooks'
import { useIsAdmin } from '@/hooks/use-admin'
import {
  useTableUrlState,
  type NavigateFn,
} from '@/hooks/use-table-url-state'
import { cn } from '@/lib/utils'
import { DataTablePage } from '@/components/data-table'
import { Input } from '@/components/ui/input'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import { getOperationLogs } from '../api'
import {
  getOperationCategoryOptions,
  getSuccessFilterOptions,
} from '../constants'
import type { OperationLog } from '../types'
import { OperationLogDetailsSheet } from './operation-log-details-sheet'
import { useOperationLogsColumns } from './operation-logs-columns'

interface OperationLogsTableProps {
  search: Record<string, unknown>
  navigate: NavigateFn
}

function getDefaultTimeRange() {
  const now = new Date()
  const start = new Date(now)
  start.setHours(0, 0, 0, 0)
  const end = new Date(now)
  end.setHours(end.getHours() + 1)
  // 与 classic 保持一致，结束时间向后留 1 小时，避免客户端/服务端轻微时钟差。
  return {
    start,
    end,
  }
}

function toTimestamp(date?: Date) {
  if (!date) return undefined
  const time = date.getTime()
  return Number.isNaN(time) ? undefined : Math.floor(time / 1000)
}

export function OperationLogsTable(props: OperationLogsTableProps) {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const [activeLog, setActiveLog] = useState<OperationLog | null>(null)
  const handleViewDetail = useCallback((log: OperationLog) => {
    setActiveLog(log)
  }, [])
  const columns = useOperationLogsColumns(isAdmin, handleViewDetail)
  const isMobile = useMediaQuery(MOBILE_MEDIA_QUERY)
  const [sorting, setSorting] = useState<SortingState>([])
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({})
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
    search: props.search,
    navigate: props.navigate,
    pagination: { defaultPage: 1, defaultPageSize: isMobile ? 10 : 20 },
    // 仅管理员可按操作者名称搜索；普通用户固定查看自身记录。
    globalFilter: { enabled: isAdmin, key: 'operatorName' },
    columnFilters: [
      { columnId: 'category', searchKey: 'category', type: 'array' },
      { columnId: 'success', searchKey: 'success', type: 'array' },
      { columnId: 'target_type', searchKey: 'targetType', type: 'string' },
      { columnId: 'target_id', searchKey: 'targetId', type: 'string' },
      { columnId: 'ip', searchKey: 'ip', type: 'string' },
    ],
  })

  const categoryFilter =
    (columnFilters.find((f) => f.id === 'category')?.value as
      | string[]
      | undefined) ?? []
  const successFilter =
    (columnFilters.find((f) => f.id === 'success')?.value as
      | string[]
      | undefined) ?? []
  const targetTypeFilter =
    (columnFilters.find((f) => f.id === 'target_type')?.value as
      | string
      | undefined) ?? ''
  const targetIdFilter =
    (columnFilters.find((f) => f.id === 'target_id')?.value as
      | string
      | undefined) ?? ''
  const ipFilter =
    (columnFilters.find((f) => f.id === 'ip')?.value as string | undefined) ??
    ''
  const setStringColumnFilter = useCallback(
    (id: string, value: string) => {
      onColumnFiltersChange((current) => {
        const next = current.filter((filter) => filter.id !== id)
        const trimmed = value.trim()
        if (trimmed) {
          next.push({ id, value: trimmed })
        }
        return next
      })
    },
    [onColumnFiltersChange]
  )

  const { data, isLoading, isFetching } = useQuery({
    queryKey: [
      'operation-logs',
      isAdmin,
      pagination.pageIndex + 1,
      pagination.pageSize,
      globalFilter,
      categoryFilter,
      successFilter,
      targetTypeFilter,
      targetIdFilter,
      ipFilter,
      startTimestamp,
      endTimestamp,
    ],
    queryFn: async () => {
      const result = await getOperationLogs(
        {
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
          operator_name: isAdmin ? globalFilter?.trim() || undefined : undefined,
          category: categoryFilter.length === 1 ? categoryFilter[0] : undefined,
          success:
            successFilter.length === 1
              ? (successFilter[0] as '1' | '0')
              : undefined,
          target_type: isAdmin ? targetTypeFilter || undefined : undefined,
          target_id: isAdmin ? targetIdFilter || undefined : undefined,
          ip: isAdmin ? ipFilter || undefined : undefined,
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

  const categoryOptions = useMemo(() => getOperationCategoryOptions(t), [t])
  const successOptions = useMemo(() => getSuccessFilterOptions(t), [t])
  const hasCustomTimeRange =
    startTimestamp !== toTimestamp(defaultTimeRange.start) ||
    endTimestamp !== toTimestamp(defaultTimeRange.end)
  const hasAdminTextFilters =
    isAdmin && Boolean(targetTypeFilter || targetIdFilter || ipFilter)

  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        isLoading={isLoading}
        isFetching={isFetching}
        emptyTitle={t('No operation logs found')}
        emptyDescription={t('Operation audit logs will appear here.')}
        skeletonKeyPrefix='operation-logs-skeleton'
        tableClassName={cn(
          'overflow-x-auto',
          isAdmin
            ? '[&_[data-slot=table]]:min-w-[1160px]'
            : '[&_[data-slot=table]]:min-w-[900px]',
          '[&_[data-slot=table-cell]:last-child]:sticky [&_[data-slot=table-cell]:last-child]:right-0 [&_[data-slot=table-cell]:last-child]:z-10 [&_[data-slot=table-cell]:last-child]:border-l [&_[data-slot=table-cell]:last-child]:bg-background',
          '[&_[data-slot=table-head]:last-child]:sticky [&_[data-slot=table-head]:last-child]:right-0 [&_[data-slot=table-head]:last-child]:z-20 [&_[data-slot=table-head]:last-child]:border-l [&_[data-slot=table-head]:last-child]:bg-muted/30',
          '[&_[data-slot=table]]:text-[13px] [&_[data-slot=table]_td]:text-[13px] [&_[data-slot=table]_td_*]:text-[13px] [&_[data-slot=table]_th]:text-[13px] [&_[data-slot=table]_th_*]:text-[13px]'
        )}
        tableHeaderClassName='bg-muted/30 sticky top-0 z-10'
        applyHeaderSize
        getRowClassName={(row) =>
          row.original.success ? undefined : 'bg-rose-50/40 dark:bg-rose-950/20'
        }
        toolbarProps={{
          // 普通用户无操作者搜索框（仅查看自身记录）。
          customSearch: isAdmin ? undefined : null,
          additionalSearch: (
            <div className='flex flex-wrap items-center gap-2'>
              <CompactDateTimeRangePicker
                start={timeRange.start}
                end={timeRange.end}
                onChange={setTimeRange}
                className='sm:w-[280px] lg:w-[360px]'
              />
              {isAdmin ? (
                <>
                  <Input
                    value={targetTypeFilter}
                    onChange={(event) =>
                      setStringColumnFilter('target_type', event.target.value)
                    }
                    placeholder={t('Target type')}
                    className='w-full sm:w-[150px]'
                  />
                  <Input
                    value={targetIdFilter}
                    onChange={(event) =>
                      setStringColumnFilter('target_id', event.target.value)
                    }
                    placeholder={t('Target ID')}
                    className='w-full sm:w-[150px]'
                  />
                  <Input
                    value={ipFilter}
                    onChange={(event) =>
                      setStringColumnFilter('ip', event.target.value)
                    }
                    placeholder={t('IP')}
                    className='w-full sm:w-[150px]'
                  />
                </>
              ) : null}
            </div>
          ),
          hasAdditionalFilters: hasCustomTimeRange || hasAdminTextFilters,
          onReset: () => setTimeRange(defaultTimeRange),
          searchPlaceholder: t('Filter by operator name...'),
          filters: [
            {
              columnId: 'category',
              title: t('Category'),
              options: categoryOptions,
              singleSelect: true,
            },
            {
              columnId: 'success',
              title: t('Result'),
              options: successOptions,
              singleSelect: true,
            },
          ],
        }}
      />
      <OperationLogDetailsSheet
        log={activeLog}
        isAdmin={isAdmin}
        onOpenChange={(open) => {
          if (!open) setActiveLog(null)
        }}
      />
    </>
  )
}
