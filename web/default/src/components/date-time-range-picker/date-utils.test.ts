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
import { describe, expect, test } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'
import zhTWMessages from '@/i18n/locales/zh-TW.json'

import {
  clampMinuteToRange,
  DATE_RANGE_WEEK_STARTS_ON,
  getCalendarLocale,
  isHourOutsideRange,
  isMinuteOutsideRange,
  isSameDay,
  setTimePart,
} from './date-utils'
import { defaultDateTimeRangePresets } from './presets'

describe('date time range helpers', () => {
  test('normalizes start and end values to inclusive minute boundaries', () => {
    const date = new Date(2026, 6, 13, 8, 30, 42, 123)
    const start = setTimePart(date, 10, 15, 'start')
    const end = setTimePart(date, 10, 15, 'end')

    expect([
      start.getHours(),
      start.getMinutes(),
      start.getSeconds(),
      start.getMilliseconds(),
    ]).toEqual([10, 15, 0, 0])
    expect([
      end.getHours(),
      end.getMinutes(),
      end.getSeconds(),
      end.getMilliseconds(),
    ]).toEqual([10, 15, 59, 999])
    expect(date.getHours()).toBe(8)
  })

  test('compares calendar dates without comparing their time', () => {
    expect(
      isSameDay(new Date(2026, 6, 13, 0, 0), new Date(2026, 6, 13, 23, 59))
    ).toBe(true)
    expect(
      isSameDay(new Date(2026, 6, 13, 23, 59), new Date(2026, 6, 14, 0, 0))
    ).toBe(false)
  })

  test('keeps an hour available when at least one minute is within the boundary', () => {
    expect(isHourOutsideRange(10, undefined, 10 * 60 + 15)).toBe(false)
    expect(isHourOutsideRange(11, undefined, 10 * 60 + 15)).toBe(true)
    expect(isHourOutsideRange(10, 10 * 60 + 45)).toBe(false)
    expect(isHourOutsideRange(9, 10 * 60 + 45)).toBe(true)
    expect(clampMinuteToRange(10, 45, undefined, 10 * 60 + 15)).toBe(15)
    expect(clampMinuteToRange(10, 15, 10 * 60 + 45)).toBe(45)
  })

  test('limits minutes within same-day start and end boundaries', () => {
    expect(isMinuteOutsideRange(10, 14, 10 * 60 + 15)).toBe(true)
    expect(isMinuteOutsideRange(10, 15, 10 * 60 + 15)).toBe(false)
    expect(isMinuteOutsideRange(10, 15, undefined, 10 * 60 + 15)).toBe(false)
    expect(isMinuteOutsideRange(10, 16, undefined, 10 * 60 + 15)).toBe(true)
  })

  test('resolves project and regional language codes to valid locales', () => {
    expect(getCalendarLocale('en-US').code).toBe('en-US')
    expect(getCalendarLocale('zh-CN').code).toBe('zh-CN')
    expect(getCalendarLocale('zhCN').code).toBe('zh-CN')
    expect(getCalendarLocale('zhTW').code).toBe('zh-TW')
    expect(toIntlLocale('zhCN')).toBe('zh-CN')
    expect(toIntlLocale('zhTW')).toBe('zh-TW')
    expect(zhTWMessages.translation['This month']).toBe('本月')
    expect(() =>
      new Intl.DateTimeFormat(toIntlLocale('zhCN'), {
        year: 'numeric',
        month: 'long',
      }).format(new Date(2026, 6, 1))
    ).not.toThrow()
  })

  test('uses the requested week boundary for the this-week preset', () => {
    const preset = defaultDateTimeRangePresets.find(
      (item) => item.id === 'this-week'
    )
    expect(preset).toBeDefined()
    if (!preset) throw new Error('This-week preset is required')

    const now = new Date(2026, 6, 15, 12, 0)
    const mondayRange = preset.getRange(now, 1)
    const sundayRange = preset.getRange(now, DATE_RANGE_WEEK_STARTS_ON)

    expect([
      mondayRange.start.getFullYear(),
      mondayRange.start.getMonth(),
      mondayRange.start.getDate(),
    ]).toEqual([2026, 6, 13])
    expect([
      sundayRange.start.getFullYear(),
      sundayRange.start.getMonth(),
      sundayRange.start.getDate(),
    ]).toEqual([2026, 6, 12])
    expect([
      mondayRange.end.getFullYear(),
      mondayRange.end.getMonth(),
      mondayRange.end.getDate(),
      mondayRange.end.getHours(),
      mondayRange.end.getMinutes(),
      mondayRange.end.getSeconds(),
    ]).toEqual([2026, 6, 19, 23, 59, 59])
  })
})
