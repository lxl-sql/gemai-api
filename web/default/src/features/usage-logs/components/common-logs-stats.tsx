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
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { useIsAdmin } from '@/hooks/use-admin'
import { formatLogQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getLogStats, getUserLogStats } from '../api'
import { DEFAULT_LOG_STATS } from '../constants'
import { buildLogStatsParams } from '../lib/utils'
import { useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')

function StatBadge(props: {
  label: string
  value: string | number
  accent: string
}) {
  return (
    <span className='border-border/60 bg-muted/25 inline-flex h-7 items-center gap-2 rounded-md border px-2.5 text-xs shadow-xs'>
      <span className={cn('h-3.5 w-0.5 rounded-full', props.accent)} />
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='text-foreground/85 font-mono font-semibold tabular-nums'>
        {props.value}
      </span>
    </span>
  )
}

// 统计后端固定按消费日志（type=2）计算；type 为 0/空 表示未筛选。
function isConsumeTypeSelection(typeParam: unknown): boolean {
  const values = Array.isArray(typeParam) ? typeParam : [typeParam]
  const active = values.filter((value) => value !== undefined && value !== '')
  if (active.length === 0) return true
  return active.every((value) => String(value) === '0' || String(value) === '2')
}

export function CommonLogsStats() {
  const { t } = useTranslation()
  const isAdmin = useIsAdmin()
  const searchParams = route.useSearch()
  const { sensitiveVisible } = useUsageLogsContext()

  const consumeTypeSelected = isConsumeTypeSelection(
    (searchParams as Record<string, unknown>).type
  )
  const { params: statsParams, unsupportedFilters } = buildLogStatsParams({
    searchParams,
    isAdmin,
  })
  const statisticsSupported = unsupportedFilters.length === 0

  const {
    data: statsResult,
    isLoading,
    isError,
    error,
  } = useQuery({
    // Keep generated default timestamps out of the key. Route filters are
    // stable, while a render-time "now" value could otherwise trigger an
    // extra rate-limited request after an unrelated re-render.
    queryKey: ['usage-logs-stats', isAdmin, searchParams],
    enabled: consumeTypeSelected && statisticsSupported,
    // 统计接口有用户级限流；429 时重试只会加剧限流，改由用户下次操作触发。
    retry: false,
    queryFn: async () => {
      const result = isAdmin
        ? await getLogStats(statsParams)
        : await getUserLogStats(statsParams)

      return {
        stats: result.data || DEFAULT_LOG_STATS,
        unavailable: !result.success,
        message: result.message,
      }
    },
  })

  if (!consumeTypeSelected) {
    return (
      <span
        className='border-border/60 bg-muted/25 text-muted-foreground inline-flex h-7 items-center rounded-md border px-2.5 text-xs'
        role='status'
      >
        {t('Statistics are only available for consume logs')}
      </span>
    )
  }

  if (!statisticsSupported) {
    return (
      <span
        className='border-border/60 bg-muted/25 text-muted-foreground inline-flex h-7 items-center rounded-md border px-2.5 text-xs'
        role='status'
      >
        {t('Statistics are unavailable for the current filters')}
      </span>
    )
  }

  if (isLoading) {
    return (
      <div className='flex items-center gap-2'>
        <Skeleton className='h-7 w-[150px] rounded-md' />
        <Skeleton className='h-7 w-[100px] rounded-md' />
        <Skeleton className='h-7 w-[120px] rounded-md' />
      </div>
    )
  }

  // 网络错误 / 超时 / 后端业务失败统一显示为不可用，绝不伪装成 0。
  if (isError || statsResult?.unavailable) {
    const status = (error as { response?: { status?: number } } | null)
      ?.response?.status
    const displayMessage =
      status === 429
        ? t('Too many requests')
        : statsResult?.message || t('Statistics temporarily unavailable')
    return (
      <span
        className='inline-flex h-7 items-center rounded-md border border-amber-500/30 bg-amber-500/10 px-2.5 text-xs text-amber-700 dark:text-amber-300'
        role='status'
        title={displayMessage}
      >
        {displayMessage}
      </span>
    )
  }

  const stats = statsResult?.stats || DEFAULT_LOG_STATS

  return (
    <div className='flex flex-wrap items-center gap-2'>
      <StatBadge
        label={t('Usage')}
        value={sensitiveVisible ? formatLogQuota(stats?.quota || 0) : '••••'}
        accent='bg-sky-500/70'
      />
      <StatBadge
        label={t('RPM')}
        value={stats?.rpm || 0}
        accent='bg-rose-500/65'
      />
      <StatBadge
        label={t('TPM')}
        value={stats?.tpm || 0}
        accent='bg-slate-400/70'
      />
    </div>
  )
}
