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
import { type ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { formatTimestampToDate } from '@/lib/format'
import { getRoleLabel } from '@/lib/roles'
import { Button } from '@/components/ui/button'
import { DataTableColumnHeader } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import {
  OPERATION_CATEGORY_VARIANTS,
  getOperationActionLabelKey,
  getOperationCategoryLabelKey,
} from '../constants'
import type { OperationLog } from '../types'

export function useOperationLogsColumns(
  isAdmin: boolean,
  onViewDetail: (log: OperationLog) => void
): ColumnDef<OperationLog>[] {
  const { t } = useTranslation()

  const columns: ColumnDef<OperationLog>[] = [
    {
      accessorKey: 'id',
      size: 72,
      meta: { label: t('ID'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('ID')} />
      ),
      cell: ({ row }) => (
        <TableId value={row.getValue('id') as number} className='w-[60px]' />
      ),
    },
    {
      accessorKey: 'created_at',
      size: 168,
      meta: { label: t('Time'), mobileTitle: !isAdmin },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Time')} />
      ),
      cell: ({ row }) => (
        <span className='font-mono text-sm whitespace-nowrap'>
          {formatTimestampToDate(row.getValue('created_at') as number)}
        </span>
      ),
    },
  ]

  // 操作者列仅对管理员有意义（普通用户只能看到自己的记录）。
  if (isAdmin) {
    columns.push({
      accessorKey: 'operator_name',
      size: 180,
      meta: { label: t('Operator'), mobileTitle: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Operator')} />
      ),
      cell: ({ row }) => {
        const log = row.original
        return (
          <div className='flex flex-col gap-0.5'>
            <div className='font-medium whitespace-nowrap'>
              {log.operator_name || '-'}
            </div>
            <div className='text-muted-foreground text-xs whitespace-nowrap'>
              {getRoleLabel(log.operator_role)}
              {log.operator_id > 0 ? ` · ID ${log.operator_id}` : ''}
            </div>
          </div>
        )
      },
    })
  }

  columns.push(
    {
      accessorKey: 'category',
      size: 120,
      meta: { label: t('Category') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Category')} />
      ),
      cell: ({ row }) => {
        const category = row.getValue('category') as string
        if (!category) return '-'
        return (
          <StatusBadge
            label={t(getOperationCategoryLabelKey(category))}
            variant={OPERATION_CATEGORY_VARIANTS[category] ?? 'neutral'}
            copyable={false}
          />
        )
      },
      filterFn: (row, id, value) =>
        (value as string[]).includes(row.getValue(id) as string),
    },
    {
      accessorKey: 'action',
      size: 150,
      meta: { label: t('Action') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Action')} />
      ),
      cell: ({ row }) => {
        const action = row.getValue('action') as string
        return (
          <span className='text-sm font-medium whitespace-nowrap'>
            {t(getOperationActionLabelKey(action))}
          </span>
        )
      },
    },
    {
      id: 'target',
      size: 140,
      meta: { label: t('Target'), mobileHidden: true },
      header: () => <span className='text-sm'>{t('Target')}</span>,
      cell: ({ row }) => {
        const log = row.original
        if (!log.target_type && !log.target_id) return '-'
        const text = `${log.target_type}${log.target_id ? ` #${log.target_id}` : ''}`
        return <span className='font-mono text-xs whitespace-nowrap'>{text}</span>
      },
    },
    {
      accessorKey: 'success',
      size: 96,
      meta: { label: t('Result'), mobileBadge: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Result')} />
      ),
      cell: ({ row }) => {
        const ok = row.getValue('success') as boolean
        return (
          <StatusBadge
            label={ok ? t('Success') : t('Failed')}
            variant={ok ? 'success' : 'danger'}
            copyable={false}
          />
        )
      },
      filterFn: (row, id, value) =>
        (value as string[]).includes((row.getValue(id) as boolean) ? '1' : '0'),
    },
  )

  // IP 仅对管理员展示（普通用户查看自身记录，IP 意义不大且略显冗余）。
  if (isAdmin) {
    columns.push({
      accessorKey: 'ip',
      size: 120,
      meta: { label: t('IP'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('IP')} />
      ),
      cell: ({ row }) => (
        <span className='font-mono text-sm'>
          {(row.getValue('ip') as string) || '-'}
        </span>
      ),
    })
  }

  columns.push({
    id: 'actions',
    size: 112,
    meta: { label: t('Actions') },
    header: () => null,
    enableHiding: false,
    enableSorting: false,
    cell: ({ row }) => {
      const log = row.original
      if (!log.detail && !log.user_agent) return '-'
      return (
        <div className='flex justify-end'>
          <Button
            variant='ghost'
            size='sm'
            className='h-7 w-fit px-2 text-xs'
            onClick={(event) => {
              event.stopPropagation()
              onViewDetail(log)
            }}
          >
            {t('View details')}
          </Button>
        </div>
      )
    },
  })

  return columns
}
