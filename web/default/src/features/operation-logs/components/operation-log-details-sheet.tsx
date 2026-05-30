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
import { useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { formatTimestampToDate } from '@/lib/format'
import { getRoleLabel } from '@/lib/roles'
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { CopyButton } from '@/components/copy-button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { StatusBadge } from '@/components/status-badge'
import {
  OPERATION_CATEGORY_VARIANTS,
  getOperationActionLabelKey,
  getOperationCategoryLabelKey,
} from '../constants'
import type { OperationLog } from '../types'

type OperationLogDetailsSheetProps = {
  log: OperationLog | null
  onOpenChange: (open: boolean) => void
  isAdmin: boolean
}

function formatJson(value: string) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return value
  }
}

function DetailItem(props: { label: string; children: ReactNode }) {
  return (
    <div className='flex min-w-0 items-start justify-between gap-3'>
      <span className='text-muted-foreground shrink-0 text-xs font-medium'>
        {props.label}
      </span>
      <div className='min-w-0 text-right text-sm wrap-break-word'>
        {props.children}
      </div>
    </div>
  )
}

export function OperationLogDetailsSheet({
  log,
  onOpenChange,
  isAdmin,
}: OperationLogDetailsSheetProps) {
  const { t } = useTranslation()
  const formattedDetail = useMemo(
    () => (log?.detail ? formatJson(log.detail) : ''),
    [log?.detail]
  )

  return (
    <Sheet open={Boolean(log)} onOpenChange={onOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-lg')}>
        {log && (
          <>
            <SheetHeader className={sideDrawerHeaderClassName()}>
              <SheetTitle className='pr-10'>{t('Details')}</SheetTitle>
              <SheetDescription className='pr-10'>
                #{log.id} · {formatTimestampToDate(log.created_at)}
              </SheetDescription>
            </SheetHeader>

            <div className={sideDrawerFormClassName('gap-5')}>
              <section className='rounded-lg border p-3'>
                <div className='flex flex-col gap-3'>
                  <DetailItem label={t('Time')}>
                    <span className='font-mono'>
                      {formatTimestampToDate(log.created_at)}
                    </span>
                  </DetailItem>
                  {isAdmin && (
                    <DetailItem label={t('Operator')}>
                      <span>
                        {log.operator_name || '-'}
                        {log.operator_id > 0
                          ? ` (${getRoleLabel(log.operator_role)} · ID: ${log.operator_id})`
                          : ''}
                      </span>
                    </DetailItem>
                  )}
                  <DetailItem label={t('Category')}>
                    <StatusBadge
                      label={t(getOperationCategoryLabelKey(log.category))}
                      variant={OPERATION_CATEGORY_VARIANTS[log.category] ?? 'neutral'}
                      copyable={false}
                    />
                  </DetailItem>
                  <DetailItem label={t('Action')}>
                    <span className='font-medium'>
                      {t(getOperationActionLabelKey(log.action))}
                    </span>
                  </DetailItem>
                  <DetailItem label={t('Target')}>
                    {log.target_type || log.target_id ? (
                      <span className='font-mono text-xs'>
                        {`${log.target_type}${log.target_id ? ` #${log.target_id}` : ''}`}
                      </span>
                    ) : (
                      '-'
                    )}
                  </DetailItem>
                  <DetailItem label={t('Result')}>
                    <StatusBadge
                      label={log.success ? t('Success') : t('Failed')}
                      variant={log.success ? 'success' : 'danger'}
                      copyable={false}
                    />
                  </DetailItem>
                  {isAdmin && log.ip && (
                    <DetailItem label={t('IP')}>
                      <span className='font-mono text-xs'>{log.ip}</span>
                    </DetailItem>
                  )}
                </div>
              </section>

              {log.user_agent && (
                <section className='flex flex-col gap-2'>
                  <div className='flex items-center justify-between gap-2'>
                    <h3 className='text-sm font-semibold'>{t('User Agent')}</h3>
                    <CopyButton value={log.user_agent} size='icon' />
                  </div>
                  <div className='bg-muted/30 text-muted-foreground rounded-lg border p-3 font-mono text-xs leading-relaxed wrap-break-word'>
                    {log.user_agent}
                  </div>
                </section>
              )}

              {formattedDetail && (
                <section className='flex flex-col gap-2'>
                  <div className='flex items-center justify-between gap-2'>
                    <h3 className='text-sm font-semibold'>{t('JSON')}</h3>
                    <CopyButton value={formattedDetail} size='icon' />
                  </div>
                  <pre className='bg-muted/30 text-foreground m-0 max-h-[380px] overflow-auto rounded-lg border p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap'>
                    {formattedDetail}
                  </pre>
                </section>
              )}
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}
