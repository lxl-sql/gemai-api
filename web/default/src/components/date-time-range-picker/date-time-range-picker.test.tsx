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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import type { DateRange } from 'react-day-picker'
import { enUS } from 'react-day-picker/locale'
import { describe, expect, test, vi } from 'vitest'

import { DateRangeCalendar } from './date-range-calendar'
import { DateTimeRangePicker } from './date-time-range-picker'
import { TimeSelect } from './time-select'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: 'en', resolvedLanguage: 'en' },
  }),
}))

function RangeCalendarHarness() {
  const [selected, setSelected] = useState<DateRange>()

  return (
    <DateRangeCalendar
      month={new Date(2026, 6, 1)}
      numberOfMonths={2}
      selected={selected}
      locale={enUS}
      startMonth={new Date(2025, 0, 1)}
      endMonth={new Date(2027, 11, 1)}
      onMonthChange={() => {}}
      onSelect={setSelected}
    />
  )
}

describe('DateTimeRangePicker', () => {
  test('selects a continuous cross-month range with one active focus target', async () => {
    const user = userEvent.setup()
    const { container } = render(<RangeCalendarHarness />)
    const getDayButton = (isoDate: string) =>
      container.querySelector<HTMLButtonElement>(
        `[data-day="${isoDate}"] button`
      )

    let startButton = getDayButton('2026-07-08')
    let endButton = getDayButton('2026-08-18')
    let middleButton = getDayButton('2026-08-01')

    expect(startButton).not.toBeNull()
    expect(endButton).not.toBeNull()
    expect(middleButton).not.toBeNull()
    if (!startButton || !endButton || !middleButton) {
      throw new Error('Expected both visible months to render')
    }

    await user.click(startButton)
    endButton = getDayButton('2026-08-18')
    if (!endButton) throw new Error('Expected end date to remain visible')
    await user.click(endButton)

    startButton = getDayButton('2026-07-08')
    endButton = getDayButton('2026-08-18')
    middleButton = getDayButton('2026-08-01')
    if (!startButton || !endButton || !middleButton) {
      throw new Error('Expected selected range to remain visible')
    }

    expect(startButton.dataset.rangeStart).toBe('true')
    expect(endButton.dataset.rangeEnd).toBe('true')
    expect(middleButton.dataset.rangeMiddle).toBe('true')
    expect(container.querySelectorAll('[data-focused="true"]')).toHaveLength(1)
    expect(startButton.closest('[data-focused="true"]')).toBeNull()
    expect(endButton.closest('[data-focused="true"]')).not.toBeNull()
  })

  test('stacks time fields on narrow layouts and constrains time menus', async () => {
    const user = userEvent.setup()
    render(
      <DateTimeRangePicker
        start={new Date(2026, 6, 16, 0, 0)}
        end={new Date(2026, 6, 17, 23, 59, 59, 999)}
        onChange={() => {}}
      />
    )

    await user.click(screen.getByRole('button', { name: /2026-07-16/ }))

    const timeFields = document.querySelector(
      '[data-slot="date-time-range-time-fields"]'
    )
    expect(timeFields?.classList.contains('grid-cols-1')).toBe(true)
    expect(timeFields?.classList.contains('sm:grid-cols-2')).toBe(true)

    await user.click(screen.getByLabelText('Start Time Hour'))
    const timeMenu = await screen.findByRole('listbox')
    const selectContent = timeMenu.closest('[data-slot="select-content"]')

    expect(selectContent?.classList.contains('h-56')).toBe(true)
    expect(selectContent?.classList.contains('max-h-56')).toBe(true)
    expect(selectContent?.classList.contains('overflow-y-auto')).toBe(true)
    expect(selectContent?.classList.contains('overscroll-contain')).toBe(true)
  })

  test('keeps boundary hours selectable and disables only invalid minutes', async () => {
    const user = userEvent.setup()
    render(
      <TimeSelect
        label='End Time'
        value={new Date(2026, 6, 16, 10, 15)}
        boundary='end'
        minimum={new Date(2026, 6, 16, 10, 15)}
        maximum={new Date(2026, 6, 16, 10, 15)}
        onChange={() => {}}
      />
    )

    await user.click(screen.getByLabelText('End Time Hour'))
    expect(
      (await screen.findByRole('option', { name: '10' })).dataset.disabled
    ).toBeUndefined()
    expect(
      (await screen.findByRole('option', { name: '09' })).dataset.disabled
    ).toBe('')
    expect(
      (await screen.findByRole('option', { name: '11' })).dataset.disabled
    ).toBe('')

    await user.keyboard('{Escape}')
    await user.click(screen.getByLabelText('End Time Minute'))
    expect(
      (await screen.findByRole('option', { name: '15' })).dataset.disabled
    ).toBeUndefined()
    expect(
      (await screen.findByRole('option', { name: '14' })).dataset.disabled
    ).toBe('')
    expect(
      (await screen.findByRole('option', { name: '16' })).dataset.disabled
    ).toBe('')
  })
})
