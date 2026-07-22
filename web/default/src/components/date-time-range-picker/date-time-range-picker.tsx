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
import { CalendarDaysIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMemo, useState } from 'react'
import type { DateRange } from 'react-day-picker'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { MOBILE_MEDIA_QUERY, useMediaQuery } from '@/hooks'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'

import { DateRangeCalendar } from './date-range-calendar'
import {
  copyDate,
  DATE_RANGE_WEEK_STARTS_ON,
  getCalendarLocale,
  isSameDay,
  setTimePart,
} from './date-utils'
import { defaultDateTimeRangePresets } from './presets'
import { TimeSelect } from './time-select'
import type { DateTimeRange, DateTimeRangePreset } from './types'

export interface DateTimeRangePickerProps {
  start?: Date
  end?: Date
  onChange: (range: DateTimeRange) => void
  presets?: DateTimeRangePreset[]
  className?: string
  disabled?: boolean
}

function copyRange(start?: Date, end?: Date): DateTimeRange {
  return { start: copyDate(start), end: copyDate(end) }
}

function normalizeCompletedRange(range: DateTimeRange): DateTimeRange {
  if (!range.start || !range.end || range.start <= range.end) return range
  if (!isSameDay(range.start, range.end)) return { start: range.start }

  return {
    start: range.start,
    end: setTimePart(
      range.end,
      range.start.getHours(),
      range.start.getMinutes(),
      'end'
    ),
  }
}

export function DateTimeRangePicker(props: DateTimeRangePickerProps) {
  const { t, i18n } = useTranslation()
  const isMobile = useMediaQuery(MOBILE_MEDIA_QUERY)
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<DateTimeRange>(() =>
    copyRange(props.start, props.end)
  )
  const [month, setMonth] = useState<Date | undefined>(props.start)
  const calendarLocale = getCalendarLocale(i18n.language)
  const presets = props.presets ?? defaultDateTimeRangePresets

  const label = useMemo(() => {
    if (!props.start && !props.end) return t('Date Range')
    const startText = props.start
      ? dayjs(props.start).format('YYYY-MM-DD HH:mm')
      : '-'
    const endText = props.end
      ? dayjs(props.end).format('YYYY-MM-DD HH:mm')
      : '-'
    return `${startText} ~ ${endText}`
  }, [props.end, props.start, t])

  const selectedRange = useMemo<DateRange | undefined>(() => {
    if (!draft.start) return undefined
    return { from: draft.start, to: draft.end }
  }, [draft.end, draft.start])

  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      setDraft(copyRange(props.start, props.end))
      const initialMonth = copyDate(props.start) ?? new Date()
      setMonth(new Date(initialMonth.getFullYear(), initialMonth.getMonth(), 1))
    }
    setOpen(nextOpen)
  }

  const handleDateSelect = (range: DateRange | undefined) => {
    if (!range?.from) {
      setDraft({})
      return
    }

    const selectedStartDate = range.from
    const selectedEndDate = range.to
    setDraft((current) => {
      const start = setTimePart(
        selectedStartDate,
        current.start?.getHours() ?? 0,
        current.start?.getMinutes() ?? 0,
        'start'
      )
      if (!selectedEndDate) return { start }

      const end = setTimePart(
        selectedEndDate,
        current.end?.getHours() ?? 23,
        current.end?.getMinutes() ?? 59,
        'end'
      )
      return normalizeCompletedRange({ start, end })
    })
  }

  const applyPreset = (preset: DateTimeRangePreset) => {
    const range = preset.getRange(new Date(), DATE_RANGE_WEEK_STARTS_ON)
    props.onChange(range)
    setOpen(false)
  }

  const clearRange = () => {
    props.onChange({})
    setOpen(false)
  }

  const applyDraft = () => {
    if (!draft.start || !draft.end || draft.start > draft.end) return
    props.onChange(copyRange(draft.start, draft.end))
    setOpen(false)
  }

  const currentYear = new Date().getFullYear()
  const visibleMonth = month ?? new Date(currentYear, new Date().getMonth(), 1)
  const calendarStartMonth = new Date(currentYear - 20, 0)
  const calendarEndMonth = new Date(currentYear + 1, 11)

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger
        disabled={props.disabled}
        render={
          <Button
            type='button'
            variant='outline'
            disabled={props.disabled}
            className={cn(
              'w-full justify-start px-2.5 text-sm leading-5 font-normal tabular-nums',
              !props.start && !props.end && 'text-muted-foreground',
              props.className
            )}
          />
        }
      >
        <HugeiconsIcon
          icon={CalendarDaysIcon}
          strokeWidth={2}
          data-icon='inline-start'
          aria-hidden='true'
        />
        <span className='truncate'>{label}</span>
      </PopoverTrigger>
      <PopoverContent
        align='start'
        collisionPadding={12}
        className='w-[min(42rem,calc(100vw-1.5rem))] gap-0 overflow-hidden p-0'
      >
        <div>
          <div className='border-b'>
            <DateRangeCalendar
              month={visibleMonth}
              numberOfMonths={isMobile ? 1 : 2}
              selected={selectedRange}
              locale={calendarLocale}
              startMonth={calendarStartMonth}
              endMonth={calendarEndMonth}
              onMonthChange={setMonth}
              onSelect={handleDateSelect}
            />
          </div>

          <div className='bg-muted/20 flex min-w-0 flex-col'>
            <div className='grid gap-3 p-3 md:grid-cols-[auto_1fr] md:items-end'>
              <div
                data-slot='date-time-range-time-fields'
                className='grid grid-cols-1 gap-3 sm:grid-cols-2'
              >
                <TimeSelect
                  label={t('Start Time')}
                  value={draft.start}
                  boundary='start'
                  maximum={draft.end}
                  onChange={(start) =>
                    setDraft((current) => ({ ...current, start }))
                  }
                />
                <TimeSelect
                  label={t('End Time')}
                  value={draft.end}
                  boundary='end'
                  minimum={draft.start}
                  onChange={(end) =>
                    setDraft((current) => ({ ...current, end }))
                  }
                />
              </div>

              <div className='grid grid-cols-2 gap-1.5 sm:grid-cols-5'>
                {presets.map((preset) => (
                  <Button
                    key={preset.id}
                    type='button'
                    variant='secondary'
                    size='sm'
                    className='justify-center px-2 text-xs font-normal'
                    onClick={() => applyPreset(preset)}
                  >
                    {t(preset.labelKey)}
                  </Button>
                ))}
              </div>
            </div>

            <div className='bg-background flex justify-between gap-2 border-t px-3 py-2'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                onClick={clearRange}
              >
                {t('Clear')}
              </Button>
              <Button
                type='button'
                size='sm'
                disabled={!draft.start || !draft.end || draft.start > draft.end}
                onClick={applyDraft}
              >
                {t('Confirm')}
              </Button>
            </div>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
