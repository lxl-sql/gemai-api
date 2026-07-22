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
import type { Locale } from 'react-day-picker'
import { enUS, fr, ja, ru, vi, zhCN, zhTW } from 'react-day-picker/locale'

import { toIntlLocale } from '@/i18n/languages'

export const DATE_RANGE_WEEK_STARTS_ON = 0

const calendarLocales: Record<string, Locale> = {
  en: enUS,
  zh: zhCN,
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  fr,
  ru,
  ja,
  vi,
}

export function getCalendarLocale(language: string): Locale {
  const locale = toIntlLocale(language)
  if (!locale) return enUS

  return (
    calendarLocales[locale] ?? calendarLocales[locale.split('-')[0]] ?? enUS
  )
}

export function isSameDay(first: Date, second: Date): boolean {
  return (
    first.getFullYear() === second.getFullYear() &&
    first.getMonth() === second.getMonth() &&
    first.getDate() === second.getDate()
  )
}

export function setTimePart(
  date: Date,
  hours: number,
  minutes: number,
  boundary: 'start' | 'end'
): Date {
  const nextDate = new Date(date)
  if (boundary === 'end') {
    nextDate.setHours(hours, minutes, 59, 999)
  } else {
    nextDate.setHours(hours, minutes, 0, 0)
  }
  return nextDate
}

export function isHourOutsideRange(
  hour: number,
  minimumMinutes?: number,
  maximumMinutes?: number
): boolean {
  const firstMinute = hour * 60
  const lastMinute = firstMinute + 59

  return (
    (minimumMinutes !== undefined && lastMinute < minimumMinutes) ||
    (maximumMinutes !== undefined && firstMinute > maximumMinutes)
  )
}

export function isMinuteOutsideRange(
  hour: number,
  minute: number,
  minimumMinutes?: number,
  maximumMinutes?: number
): boolean {
  const candidateMinutes = hour * 60 + minute

  return (
    (minimumMinutes !== undefined && candidateMinutes < minimumMinutes) ||
    (maximumMinutes !== undefined && candidateMinutes > maximumMinutes)
  )
}

export function clampMinuteToRange(
  hour: number,
  minute: number,
  minimumMinutes?: number,
  maximumMinutes?: number
): number {
  let nextMinute = minute

  if (
    minimumMinutes !== undefined &&
    Math.floor(minimumMinutes / 60) === hour
  ) {
    nextMinute = Math.max(nextMinute, minimumMinutes % 60)
  }
  if (
    maximumMinutes !== undefined &&
    Math.floor(maximumMinutes / 60) === hour
  ) {
    nextMinute = Math.min(nextMinute, maximumMinutes % 60)
  }

  return nextMinute
}

export function copyDate(date?: Date): Date | undefined {
  return date ? new Date(date) : undefined
}
