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
import {
  ArrowLeft01Icon,
  ArrowLeftDoubleIcon,
  ArrowRight01Icon,
  ArrowRightDoubleIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import {
  useDayPicker,
  type DateRange,
  type DayButtonProps,
  type Locale,
  type MonthCaptionProps,
} from 'react-day-picker'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Calendar, CalendarDayButton } from '@/components/ui/calendar'
import { toIntlLocale } from '@/i18n/languages'
import { cn } from '@/lib/utils'

import { DATE_RANGE_WEEK_STARTS_ON } from './date-utils'

interface DateRangeCalendarProps {
  month: Date
  numberOfMonths: 1 | 2
  selected?: DateRange
  locale: Locale
  startMonth: Date
  endMonth: Date
  onMonthChange: (month: Date) => void
  onSelect: (range: DateRange | undefined) => void
}

function shiftMonth(month: Date, offset: number): Date {
  return new Date(month.getFullYear(), month.getMonth() + offset, 1)
}

function DateRangeMonthCaption(props: MonthCaptionProps) {
  const { t, i18n } = useTranslation()
  const dayPicker = useDayPicker()
  const firstVisibleMonth = dayPicker.months[0]?.date
  const visibleMonthCount = dayPicker.months.length
  const startMonth = dayPicker.dayPickerProps.startMonth
  const endMonth = dayPicker.dayPickerProps.endMonth
  const latestFirstMonth = endMonth
    ? shiftMonth(endMonth, -(visibleMonthCount - 1))
    : undefined
  const monthLabel = new Intl.DateTimeFormat(
    toIntlLocale(i18n.resolvedLanguage || i18n.language),
    { year: 'numeric', month: 'long' }
  ).format(props.calendarMonth.date)

  const canNavigate = (offset: number) => {
    if (!firstVisibleMonth) return false
    const candidate = shiftMonth(firstVisibleMonth, offset)
    if (startMonth && candidate < startMonth) return false
    return !latestFirstMonth || candidate <= latestFirstMonth
  }

  const navigate = (offset: number) => {
    if (!firstVisibleMonth || !canNavigate(offset)) return
    dayPicker.goToMonth(shiftMonth(firstVisibleMonth, offset))
  }

  return (
    <header
      className={cn(
        props.className,
        'grid grid-cols-[auto_1fr_auto] items-center gap-2 px-2 py-1.5'
      )}
    >
      <div className='flex items-center'>
        <Button
          type='button'
          variant='ghost'
          size='icon-xs'
          disabled={!canNavigate(-12)}
          aria-label={t('Previous year')}
          onClick={() => navigate(-12)}
        >
          <HugeiconsIcon
            icon={ArrowLeftDoubleIcon}
            strokeWidth={2}
            aria-hidden='true'
          />
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='icon-xs'
          disabled={!canNavigate(-1)}
          aria-label={t('Previous month')}
          onClick={() => navigate(-1)}
        >
          <HugeiconsIcon
            icon={ArrowLeft01Icon}
            strokeWidth={2}
            aria-hidden='true'
          />
        </Button>
      </div>

      <div
        role='status'
        aria-live='polite'
        className='truncate text-center text-sm font-semibold tabular-nums'
      >
        {monthLabel}
      </div>

      <div className='flex items-center justify-end'>
        <Button
          type='button'
          variant='ghost'
          size='icon-xs'
          disabled={!canNavigate(1)}
          aria-label={t('Next month')}
          onClick={() => navigate(1)}
        >
          <HugeiconsIcon
            icon={ArrowRight01Icon}
            strokeWidth={2}
            aria-hidden='true'
          />
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='icon-xs'
          disabled={!canNavigate(12)}
          aria-label={t('Next year')}
          onClick={() => navigate(12)}
        >
          <HugeiconsIcon
            icon={ArrowRightDoubleIcon}
            strokeWidth={2}
            aria-hidden='true'
          />
        </Button>
      </div>
    </header>
  )
}

function DateRangeDayButton(props: DayButtonProps) {
  return (
    <CalendarDayButton
      {...props}
      className={cn(
        props.className,
        'size-(--cell-size) min-w-(--cell-size) rounded-full data-[range-end=true]:rounded-full data-[range-middle=true]:rounded-full data-[range-middle=true]:bg-transparent data-[range-start=true]:rounded-full',
        'group-data-[focused=true]/day:not-focus-visible:border-transparent group-data-[focused=true]/day:not-focus-visible:ring-0'
      )}
    />
  )
}

export function DateRangeCalendar(props: DateRangeCalendarProps) {
  const latestVisibleMonth = shiftMonth(
    props.endMonth,
    -(props.numberOfMonths - 1)
  )
  let visibleMonth = props.month
  if (visibleMonth < props.startMonth) visibleMonth = props.startMonth
  if (visibleMonth > latestVisibleMonth) visibleMonth = latestVisibleMonth

  return (
    <Calendar
      mode='range'
      fixedWeeks
      hideNavigation
      showOutsideDays={false}
      weekStartsOn={DATE_RANGE_WEEK_STARTS_ON}
      numberOfMonths={props.numberOfMonths}
      month={visibleMonth}
      onMonthChange={props.onMonthChange}
      selected={props.selected}
      onSelect={props.onSelect}
      locale={props.locale}
      startMonth={props.startMonth}
      endMonth={props.endMonth}
      className='w-full p-0 [--cell-size:--spacing(8)]'
      classNames={{
        months: 'grid w-full grid-cols-1 gap-0 md:grid-cols-2 md:divide-x',
        month: 'flex min-w-0 flex-col gap-0',
        month_caption: 'border-b',
        month_grid: 'my-2 w-full border-collapse',
        weekdays: 'grid w-full grid-cols-7 px-2',
        weekday:
          'text-muted-foreground flex h-(--cell-size) min-w-0 items-center justify-center text-center text-[0.8rem] font-normal select-none',
        week: 'mt-1 grid w-full grid-cols-7 px-2',
        day: 'group/day relative flex h-(--cell-size) min-w-0 items-center justify-center p-0 text-center select-none [&:first-child[data-selected=true]]:rounded-l-full [&:last-child[data-selected=true]]:rounded-r-full',
        range_start:
          'after:absolute after:inset-y-0 after:right-0 after:left-1/2 after:bg-accent [&.rdp-range_end]:after:hidden [&:last-child]:after:rounded-r-full',
        range_middle: 'bg-accent',
        range_end:
          'before:absolute before:inset-y-0 before:right-1/2 before:left-0 before:bg-accent [&.rdp-range_start]:before:hidden [&:first-child]:before:rounded-l-full',
        today:
          'text-foreground [&>button:not([data-range-start=true]):not([data-range-end=true]):not([data-range-middle=true])]:bg-muted',
      }}
      components={{
        MonthCaption: DateRangeMonthCaption,
        DayButton: DateRangeDayButton,
      }}
    />
  )
}
