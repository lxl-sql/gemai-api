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
import { Clock01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Field, FieldTitle } from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

import {
  clampMinuteToRange,
  isHourOutsideRange,
  isMinuteOutsideRange,
  isSameDay,
  setTimePart,
} from './date-utils'

const hourItems = Array.from({ length: 24 }, (_, hour) => {
  const value = String(hour).padStart(2, '0')
  return { value, label: value }
})

const minuteItems = Array.from({ length: 60 }, (_, minute) => {
  const value = String(minute).padStart(2, '0')
  return { value, label: value }
})

interface TimeSelectProps {
  label: string
  value?: Date
  boundary: 'start' | 'end'
  minimum?: Date
  maximum?: Date
  onChange: (value: Date) => void
}

export function TimeSelect(props: TimeSelectProps) {
  const { t } = useTranslation()
  const hourId = useId()
  const minuteId = useId()
  const hour = String(props.value?.getHours() ?? 0).padStart(2, '0')
  const minute = String(props.value?.getMinutes() ?? 0).padStart(2, '0')

  const minimumMinutes =
    props.value && props.minimum && isSameDay(props.value, props.minimum)
      ? props.minimum.getHours() * 60 + props.minimum.getMinutes()
      : undefined
  const maximumMinutes =
    props.value && props.maximum && isSameDay(props.value, props.maximum)
      ? props.maximum.getHours() * 60 + props.maximum.getMinutes()
      : undefined

  const updateTime = (nextHour: number, nextMinute: number) => {
    if (!props.value) return
    props.onChange(
      setTimePart(props.value, nextHour, nextMinute, props.boundary)
    )
  }

  return (
    <Field className='gap-1.5' data-disabled={!props.value || undefined}>
      <FieldTitle className='text-muted-foreground text-xs'>
        <HugeiconsIcon
          icon={Clock01Icon}
          strokeWidth={2}
          className='size-3.5'
          aria-hidden='true'
        />
        {props.label}
      </FieldTitle>
      <div className='flex items-center gap-1.5'>
        <Select
          items={hourItems}
          value={hour}
          disabled={!props.value}
          onValueChange={(value) => {
            if (value === null) return
            const nextHour = Number(value)
            const nextMinute = clampMinuteToRange(
              nextHour,
              Number(minute),
              minimumMinutes,
              maximumMinutes
            )
            updateTime(nextHour, nextMinute)
          }}
        >
          <SelectTrigger
            id={hourId}
            size='sm'
            className='w-[4.5rem] font-mono'
            aria-label={`${props.label} ${t('Hour')}`}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent
            alignItemWithTrigger={false}
            className='h-56 max-h-56 min-w-[5rem] overflow-y-auto overscroll-contain'
          >
            <SelectGroup>
              {hourItems.map((item) => {
                const disabled = isHourOutsideRange(
                  Number(item.value),
                  minimumMinutes,
                  maximumMinutes
                )
                return (
                  <SelectItem
                    key={item.value}
                    value={item.value}
                    disabled={disabled}
                    className='font-mono'
                  >
                    {item.label}
                  </SelectItem>
                )
              })}
            </SelectGroup>
          </SelectContent>
        </Select>
        <span className='text-muted-foreground'>:</span>
        <Select
          items={minuteItems}
          value={minute}
          disabled={!props.value}
          onValueChange={(value) => {
            if (value !== null) updateTime(Number(hour), Number(value))
          }}
        >
          <SelectTrigger
            id={minuteId}
            size='sm'
            className='w-[4.5rem] font-mono'
            aria-label={`${props.label} ${t('Minute')}`}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent
            alignItemWithTrigger={false}
            className='h-56 max-h-56 min-w-[5rem] overflow-y-auto overscroll-contain'
          >
            <SelectGroup>
              {minuteItems.map((item) => {
                const disabled = isMinuteOutsideRange(
                  Number(hour),
                  Number(item.value),
                  minimumMinutes,
                  maximumMinutes
                )
                return (
                  <SelectItem
                    key={item.value}
                    value={item.value}
                    disabled={disabled}
                    className='font-mono'
                  >
                    {item.label}
                  </SelectItem>
                )
              })}
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>
    </Field>
  )
}
