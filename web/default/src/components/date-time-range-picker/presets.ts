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
import dayjs from '@/lib/dayjs'

import type { DateTimeRangePreset } from './types'

export const defaultDateTimeRangePresets: DateTimeRangePreset[] = [
  {
    id: 'today',
    labelKey: 'Today',
    getRange: (now) => ({
      start: dayjs(now).startOf('day').toDate(),
      end: dayjs(now).endOf('day').toDate(),
    }),
  },
  {
    id: 'last-7-days',
    labelKey: '7 Days',
    getRange: (now) => ({
      start: dayjs(now).subtract(6, 'day').startOf('day').toDate(),
      end: dayjs(now).endOf('day').toDate(),
    }),
  },
  {
    id: 'this-week',
    labelKey: 'This week',
    getRange: (now, weekStartsOn) => {
      const currentDay = dayjs(now)
      const daysSinceWeekStart = (currentDay.day() - weekStartsOn + 7) % 7
      const start = currentDay
        .subtract(daysSinceWeekStart, 'day')
        .startOf('day')
      return {
        start: start.toDate(),
        end: start.add(6, 'day').endOf('day').toDate(),
      }
    },
  },
  {
    id: 'last-30-days',
    labelKey: '30 Days',
    getRange: (now) => ({
      start: dayjs(now).subtract(29, 'day').startOf('day').toDate(),
      end: dayjs(now).endOf('day').toDate(),
    }),
  },
  {
    id: 'this-month',
    labelKey: 'This month',
    getRange: (now) => ({
      start: dayjs(now).startOf('month').toDate(),
      end: dayjs(now).endOf('month').toDate(),
    }),
  },
]
