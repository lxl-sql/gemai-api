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
import { Minus, Plus } from 'lucide-react'
import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

interface NumericSpinnerInputProps {
  value: number | null | undefined
  onChange: (value: number) => void | Promise<boolean>
  min?: number
  max?: number
  step?: number
  disabled?: boolean
  className?: string
  label?: string
}

export function NumericSpinnerInput({
  value,
  onChange,
  min = 0,
  max,
  step = 1,
  disabled = false,
  className,
  label,
}: NumericSpinnerInputProps) {
  const { t } = useTranslation()
  const [localValue, setLocalValue] = useState(String(value ?? 0))
  const [editing, setEditing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [optimisticValue, setOptimisticValue] = useState<{
    value: number
    serverValue: number
  } | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const currentValue = value ?? 0
  const displayedValue = optimisticValue?.value ?? currentValue

  useEffect(() => {
    if (editing || saving) {
      return
    }
    if (
      optimisticValue &&
      currentValue !== optimisticValue.value &&
      currentValue === optimisticValue.serverValue
    ) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setLocalValue(String(optimisticValue.value))
      return
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setOptimisticValue(null)
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLocalValue(String(currentValue))
  }, [currentValue, editing, optimisticValue, saving])

  const clamp = (v: number) => {
    let result = v
    if (min !== undefined) result = Math.max(min, result)
    if (max !== undefined) result = Math.min(max, result)
    return result
  }

  const submitValue = (next: number) => {
    const previousValue = displayedValue
    const serverValue = optimisticValue?.serverValue ?? currentValue
    const result = onChange(next)
    if (!result) {
      setLocalValue(String(previousValue))
      return
    }

    setOptimisticValue({ value: next, serverValue })
    setSaving(true)
    const rollback = () => {
      setOptimisticValue(
        previousValue === serverValue
          ? null
          : { value: previousValue, serverValue }
      )
      setLocalValue(String(previousValue))
    }
    void result
      .then((saved) => {
        if (!saved) {
          rollback()
        }
      })
      .catch(rollback)
      .finally(() => {
        setSaving(false)
      })
  }

  const handleIncrement = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (disabled || saving) return
    const next = clamp((Number(localValue) || 0) + step)
    setLocalValue(String(next))
    submitValue(next)
  }

  const handleDecrement = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (disabled || saving) return
    const next = clamp((Number(localValue) || 0) - step)
    setLocalValue(String(next))
    submitValue(next)
  }

  const handleStartEdit = () => {
    if (disabled || saving) return
    setEditing(true)
    requestAnimationFrame(() => inputRef.current?.select())
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const raw = e.target.value
    if (raw === '' || raw === '-') {
      setLocalValue(raw)
      return
    }
    if (!/^-?\d+$/.test(raw)) return
    setLocalValue(raw)
  }

  const commitValue = () => {
    setEditing(false)
    const num = Number(localValue)
    if (Number.isNaN(num) || localValue === '' || localValue === '-') {
      setLocalValue(String(displayedValue))
      return
    }
    const clamped = clamp(num)
    setLocalValue(String(clamped))
    if (clamped !== displayedValue) {
      submitValue(clamped)
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      commitValue()
    } else if (e.key === 'Escape') {
      setEditing(false)
      setLocalValue(String(displayedValue))
    }
  }

  const atMin = min !== undefined && Number(localValue) <= min
  const atMax = max !== undefined && Number(localValue) >= max
  const inputDisabled = disabled || saving

  return (
    <div className={cn('inline-flex items-center', className)}>
      {label && (
        <Label className='text-muted-foreground mr-1.5 text-xs'>{label}</Label>
      )}
      <div
        className={cn(
          'group/spinner border-input inline-flex h-7 items-center gap-0 rounded-md border transition-colors',
          !disabled && 'hover:bg-muted/60',
          editing && 'bg-muted/60 ring-primary/30 ring-1'
        )}
      >
        <button
          type='button'
          tabIndex={-1}
          aria-label={t('Decrement')}
          onClick={handleDecrement}
          disabled={inputDisabled || atMin}
          className={cn(
            'text-muted-foreground/0 group-hover/spinner:text-muted-foreground flex h-7 w-6 shrink-0 items-center justify-center rounded-l-md transition-colors',
            !inputDisabled &&
              !atMin &&
              'group-hover/spinner:hover:text-foreground group-hover/spinner:hover:bg-muted',
            (inputDisabled || atMin) && 'group-hover/spinner:opacity-30'
          )}
        >
          <Minus className='size-3' />
        </button>

        {editing ? (
          <input
            ref={inputRef}
            type='text'
            value={localValue}
            onChange={handleInputChange}
            onBlur={commitValue}
            onKeyDown={handleKeyDown}
            className='h-7 w-10 bg-transparent text-center font-mono text-sm outline-none'
            autoFocus
          />
        ) : (
          <button
            type='button'
            onClick={handleStartEdit}
            disabled={inputDisabled}
            title={localValue}
            className={cn(
              'h-7 min-w-8 max-w-16 cursor-text truncate px-1 text-center font-mono text-sm tabular-nums',
              inputDisabled && 'cursor-default opacity-50'
            )}
          >
            {localValue}
          </button>
        )}

        <button
          type='button'
          tabIndex={-1}
          aria-label={t('Increment')}
          onClick={handleIncrement}
          disabled={inputDisabled || atMax}
          className={cn(
            'text-muted-foreground/0 group-hover/spinner:text-muted-foreground flex h-7 w-6 shrink-0 items-center justify-center rounded-r-md transition-colors',
            !inputDisabled &&
              !atMax &&
              'group-hover/spinner:hover:text-foreground group-hover/spinner:hover:bg-muted',
            (inputDisabled || atMax) && 'group-hover/spinner:opacity-30'
          )}
        >
          <Plus className='size-3' />
        </button>
      </div>
    </div>
  )
}
