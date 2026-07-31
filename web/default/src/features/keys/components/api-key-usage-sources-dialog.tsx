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
import dayjs from 'dayjs'
import { AlertCircle, ChevronLeft, ChevronRight, Loader2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'

import { getTokenUsageSources } from '../api'
import { parseClientLabel } from '../lib/user-agent'
import { useApiKeys } from './api-keys-provider'

const pageSize = 50

function formatTimestamp(timestamp: number): string {
  if (timestamp <= 0) return '—'
  return dayjs.unix(timestamp).format('YYYY-MM-DD HH:mm:ss')
}

export function ApiKeyUsageSourcesDialog() {
  const { t, i18n } = useTranslation()
  const { open, setOpen, currentRow } = useApiKeys()
  const [page, setPage] = useState(1)
  const isOpen = open === 'usage-sources' && currentRow !== null

  useEffect(() => {
    if (isOpen) setPage(1)
  }, [currentRow?.id, isOpen])

  const query = useQuery({
    queryKey: ['api-key-usage-sources', currentRow?.id, page],
    enabled: isOpen,
    staleTime: 15_000,
    refetchInterval: 5_000,
    queryFn: async () => {
      if (!currentRow) throw new Error(t('API key not found'))
      const response = await getTokenUsageSources(currentRow.id, page, pageSize)
      if (!response.success || !response.data) {
        throw new Error(response.message || t('Failed to load usage sources'))
      }
      return response.data
    },
  })

  const totalPages = Math.max(1, Math.ceil((query.data?.total ?? 0) / pageSize))
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const countFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale])
  const description = currentRow
    ? t('IP addresses and clients observed for {{name}}.', {
        name: currentRow.name,
      })
    : undefined

  return (
    <Dialog
      open={isOpen}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) setOpen(null)
      }}
      title={t('Usage Sources')}
      description={description}
      contentClassName='sm:max-w-6xl'
      contentHeight='min(66vh, 38rem)'
    >
      <div className='space-y-4'>
        {query.isLoading ? (
          <div className='text-muted-foreground flex min-h-48 items-center justify-center gap-2'>
            <Loader2 className='size-4 animate-spin' aria-hidden='true' />
            <span>{t('Loading usage sources...')}</span>
          </div>
        ) : null}

        {query.isError ? (
          <div
            role='alert'
            className='border-destructive/30 bg-destructive/5 text-destructive flex min-h-32 items-center justify-center gap-2 rounded-lg border p-4'
          >
            <AlertCircle className='size-4' aria-hidden='true' />
            <span>
              {query.error instanceof Error
                ? query.error.message
                : t('Failed to load usage sources')}
            </span>
          </div>
        ) : null}

        {query.data ? (
          <>
            <div className='bg-muted/30 flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg border px-3 py-2 text-xs'>
              {query.data.tracking_enabled ? (
                <Badge variant='secondary'>{t('Tracking enabled')}</Badge>
              ) : null}
              {query.data.available ? (
                <Badge variant='secondary'>
                  {t('Direct request counting')}
                </Badge>
              ) : null}
              {query.data.truncated ? (
                <Badge variant='outline'>{t('Recent sources only')}</Badge>
              ) : null}
              {query.data.tracking_start > 0 ? (
                <span className='text-muted-foreground'>
                  {t('Tracked since {{time}}', {
                    time: formatTimestamp(query.data.tracking_start),
                  })}
                </span>
              ) : null}
              {query.data.available ? (
                <span className='text-muted-foreground'>
                  {t('New requests update within {{seconds}} seconds', {
                    seconds: query.data.update_interval_seconds,
                  })}
                </span>
              ) : null}
            </div>

            {!query.data.available ? (
              <div className='bg-muted/50 rounded-lg border p-4 text-sm'>
                {t('Usage source tracking is not enabled on this deployment.')}
              </div>
            ) : null}

            {query.data.available && !query.data.tracking_enabled ? (
              <div className='bg-muted/50 rounded-lg border p-4 text-sm'>
                {t('Source tracking is being initialized for this API Key.')}
              </div>
            ) : null}

            {query.data.available &&
            query.data.tracking_enabled &&
            query.data.items.length === 0 ? (
              <div className='text-muted-foreground flex min-h-40 items-center justify-center rounded-lg border border-dashed p-4 text-sm'>
                {t('No usage sources have been recorded yet.')}
              </div>
            ) : null}

            {query.data.items.length > 0 ? (
              <div className='overflow-x-auto rounded-lg border'>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('IP Address')}</TableHead>
                      <TableHead>{t('Client')}</TableHead>
                      <TableHead>{t('Requests')}</TableHead>
                      <TableHead>{t('First Seen')}</TableHead>
                      <TableHead>{t('Last Seen')}</TableHead>
                      <TableHead>{t('Last Success')}</TableHead>
                      <TableHead>{t('Last Error')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {query.data.items.map((source) => (
                      <TableRow key={`${source.ip}\u0000${source.user_agent}`}>
                        <TableCell className='font-mono'>
                          {source.ip || '—'}
                        </TableCell>
                        <TableCell className='max-w-80'>
                          {source.user_agent ? (
                            <TooltipProvider delay={200}>
                              <Tooltip>
                                <TooltipTrigger
                                  render={<span className='block truncate' />}
                                >
                                  {parseClientLabel(source.user_agent)}
                                </TooltipTrigger>
                                <TooltipContent
                                  side='top'
                                  className='max-w-sm break-all whitespace-normal'
                                >
                                  {source.user_agent}
                                </TooltipContent>
                              </Tooltip>
                            </TooltipProvider>
                          ) : (
                            '—'
                          )}
                        </TableCell>
                        <TableCell>
                          <div className='flex min-w-36 flex-col gap-0.5'>
                            <span className='font-medium'>
                              {t('{{count}} total', {
                                count: countFormatter.format(
                                  source.request_count
                                ),
                              })}
                            </span>
                            <span className='text-muted-foreground text-xs'>
                              {t('{{success}} succeeded · {{error}} failed', {
                                success: countFormatter.format(
                                  source.success_count
                                ),
                                error: countFormatter.format(
                                  source.error_count
                                ),
                              })}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell>
                          {formatTimestamp(source.first_seen_at)}
                        </TableCell>
                        <TableCell>
                          {formatTimestamp(source.last_seen_at)}
                        </TableCell>
                        <TableCell>
                          {formatTimestamp(source.last_success_at)}
                        </TableCell>
                        <TableCell>
                          {formatTimestamp(source.last_error_at)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : null}

            {query.data.total > pageSize ? (
              <div className='flex items-center justify-end gap-2'>
                <span className='text-muted-foreground text-sm'>
                  {t('Page {{page}} of {{total}}', {
                    page,
                    total: totalPages,
                  })}
                </span>
                <Button
                  type='button'
                  variant='outline'
                  size='icon-sm'
                  aria-label={t('Previous page')}
                  disabled={page <= 1 || query.isFetching}
                  onClick={() => setPage((current) => Math.max(1, current - 1))}
                >
                  <ChevronLeft aria-hidden='true' />
                </Button>
                <Button
                  type='button'
                  variant='outline'
                  size='icon-sm'
                  aria-label={t('Next page')}
                  disabled={page >= totalPages || query.isFetching}
                  onClick={() =>
                    setPage((current) => Math.min(totalPages, current + 1))
                  }
                >
                  <ChevronRight aria-hidden='true' />
                </Button>
              </div>
            ) : null}
          </>
        ) : null}
      </div>
    </Dialog>
  )
}
