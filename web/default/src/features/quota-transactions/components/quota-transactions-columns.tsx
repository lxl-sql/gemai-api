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
import type { ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { formatQuota, formatTimestampToDate, quotaUnitsToDollars, parseQuotaFromDollars } from '@/lib/format'
import { DataTableColumnHeader } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip'
import {
  QUOTA_TRANSACTION_TYPE_VARIANTS,
  getQuotaTransactionTypeLabelKey,
  getQuotaTransactionSourceLabelKey,
  getQuotaTransactionReferenceTypeLabelKey,
} from '../constants'
import type { QuotaTransaction } from '../types'

function formatDelta(value: number) {
  if (value === 0) return '-'
  const prefix = value > 0 ? '+' : ''
  return `${prefix}${formatQuota(value)}`
}

function deltaVariant(value: number) {
  if (value > 0) return 'success' as const
  if (value < 0) return 'danger' as const
  return 'neutral' as const
}

export function useQuotaTransactionsColumns(
  isAdmin: boolean
): ColumnDef<QuotaTransaction>[] {
  const { t } = useTranslation()

  const columns: ColumnDef<QuotaTransaction>[] = [
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

  if (isAdmin) {
    columns.push({
      accessorKey: 'username',
      size: 160,
      meta: { label: t('User'), mobileTitle: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('User')} />
      ),
      cell: ({ row }) => {
        const tx = row.original
        return (
          <div className='flex flex-col gap-0.5'>
            <span className='font-medium whitespace-nowrap'>
              {tx.username || '-'}
            </span>
            <span className='text-muted-foreground font-mono text-xs'>
              ID {tx.user_id}
            </span>
          </div>
        )
      },
    })
  }

  columns.push(
    {
      id: 'direction',
      accessorFn: (row) => (row.total_delta >= 0 ? 'income' : 'expense'),
      meta: { label: t('Direction') },
      header: () => null,
      cell: () => null,
      filterFn: (row, id, value) =>
        (value as string[]).includes(row.getValue(id) as string),
    },
    {
      id: 'bucket',
      accessorFn: (row) => {
        if (row.quota_delta !== 0 && row.gift_quota_delta !== 0) return 'both'
        if (row.quota_delta !== 0) return 'recharge'
        if (row.gift_quota_delta !== 0) return 'gift'
        return ''
      },
      meta: { label: t('Quota Type') },
      header: () => null,
      cell: () => null,
      filterFn: (row, id, value) =>
        (value as string[]).includes(row.getValue(id) as string),
    },
    {
      accessorKey: 'type',
      size: 150,
      meta: { label: t('Type'), mobileBadge: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Type')} />
      ),
      cell: ({ row }) => {
        const type = row.getValue('type') as string
        return (
          <StatusBadge
            label={t(getQuotaTransactionTypeLabelKey(type))}
            variant={QUOTA_TRANSACTION_TYPE_VARIANTS[type] ?? 'neutral'}
            copyable={false}
          />
        )
      },
      filterFn: (row, id, value) =>
        (value as string[]).includes(row.getValue(id) as string),
    },
    {
      id: 'quota_change',
      size: 160,
      meta: { label: t('Quota Delta') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Quota Delta')} />
      ),
      cell: ({ row }) => {
        const tx = row.original
        const quotaDelta = tx.quota_delta || 0
        const giftQuotaDelta = tx.gift_quota_delta || 0

        if (quotaDelta !== 0 && giftQuotaDelta !== 0) {
          const dollarsQuota = quotaUnitsToDollars(quotaDelta)
          const dollarsGift = quotaUnitsToDollars(giftQuotaDelta)
          const roundedQuota = Number(dollarsQuota.toFixed(2))
          const roundedGift = Number(dollarsGift.toFixed(2))
          const roundedTotal = roundedQuota + roundedGift
          const totalUnits = parseQuotaFromDollars(roundedTotal)

          return (
            <div className='flex flex-col gap-1.5 py-1 text-xs font-mono'>
              <div className='flex items-center gap-1.5'>
                <span className='text-muted-foreground text-[10px]'>{t('Recharge')}:</span>
                <StatusBadge
                  label={formatDelta(quotaDelta)}
                  variant={deltaVariant(quotaDelta)}
                  copyable={false}
                />
              </div>
              <div className='flex items-center gap-1.5'>
                <span className='text-muted-foreground text-[10px]'>{t('Gift')}:</span>
                <StatusBadge
                  label={formatDelta(giftQuotaDelta)}
                  variant={deltaVariant(giftQuotaDelta)}
                  copyable={false}
                />
              </div>
              <div className='flex items-center gap-1.5 font-semibold border-t border-dashed pt-1 mt-0.5 border-border'>
                <span className='text-[10px]'>{t('Total')}:</span>
                <span className={roundedTotal > 0 ? 'text-green-600 dark:text-green-400' : 'text-destructive'}>
                  {formatDelta(totalUnits)}
                </span>
              </div>
            </div>
          )
        }

        if (quotaDelta !== 0) {
          return (
            <div className='flex items-center gap-1.5 font-mono text-xs'>
              <span className='text-muted-foreground text-[10px]'>{t('Recharge')}:</span>
              <StatusBadge
                label={formatDelta(quotaDelta)}
                variant={deltaVariant(quotaDelta)}
                copyable={false}
              />
            </div>
          )
        }

        if (giftQuotaDelta !== 0) {
          return (
            <div className='flex items-center gap-1.5 font-mono text-xs'>
              <span className='text-muted-foreground text-[10px]'>{t('Gift')}:</span>
              <StatusBadge
                label={formatDelta(giftQuotaDelta)}
                variant={deltaVariant(giftQuotaDelta)}
                copyable={false}
              />
            </div>
          )
        }

        return <span className='text-muted-foreground font-mono text-xs'>-</span>
      },
    },
    {
      id: 'balance_after',
      size: 190,
      meta: { label: t('Balance After'), mobileHidden: true },
      header: () => <span className='text-sm'>{t('Balance After')}</span>,
      cell: ({ row }) => {
        const tx = row.original
        const total = tx.balance_after + tx.gift_balance_after
        return (
          <Tooltip>
            <TooltipTrigger
              render={<div className='w-[150px] cursor-help space-y-1' />}
            >
              <div className='flex justify-between text-xs'>
                <span className='font-medium tabular-nums'>
                  {formatQuota(total)}
                </span>
              </div>
              <div className='text-muted-foreground flex justify-between text-[10px] tabular-nums'>
                <span>{formatQuota(tx.balance_after)}</span>
                <span>{formatQuota(tx.gift_balance_after)}</span>
              </div>
            </TooltipTrigger>
            <TooltipContent>
              <div className='space-y-1 text-xs'>
                <div>
                  {t('Recharge quota:')} {formatQuota(tx.balance_after)}
                </div>
                <div>
                  {t('Gift quota:')} {formatQuota(tx.gift_balance_after)}
                </div>
                <div>
                  {t('Balance After:')} {formatQuota(total)}
                </div>
              </div>
            </TooltipContent>
          </Tooltip>
        )
      },
    },
    {
      id: 'reference',
      size: 170,
      meta: { label: t('Reference'), mobileHidden: true },
      header: () => <span className='text-sm'>{t('Reference')}</span>,
      cell: ({ row }) => {
        const tx = row.original
        const refTypeLabel = t(getQuotaTransactionReferenceTypeLabelKey(tx.reference_type))
        const reference = tx.reference_id ? `${refTypeLabel} #${tx.reference_id}` : refTypeLabel

        return (
          <div className='flex flex-col gap-0.5'>
            <span className='font-mono text-xs whitespace-nowrap'>
              {reference || '-'}
            </span>
            {tx.source ? (
              <span className='text-muted-foreground text-xs whitespace-nowrap'>
                {t(getQuotaTransactionSourceLabelKey(tx.source))}
              </span>
            ) : null}
          </div>
        )
      },
    }
  )

  if (isAdmin) {
    columns.push({
      id: 'operator',
      size: 150,
      meta: { label: t('Operator'), mobileHidden: true },
      header: () => <span className='text-sm'>{t('Operator')}</span>,
      cell: ({ row }) => {
        const tx = row.original
        if (!tx.operator_id) return '-'
        return (
          <div className='flex flex-col gap-0.5'>
            <span className='font-medium whitespace-nowrap'>
              {tx.operator_name || '-'}
            </span>
            <span className='text-muted-foreground font-mono text-xs'>
              ID {tx.operator_id}
            </span>
          </div>
        )
      },
    })
  }

  return columns
}
