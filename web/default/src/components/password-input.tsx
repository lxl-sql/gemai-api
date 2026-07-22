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
import { Eye, EyeOff } from 'lucide-react'
import * as React from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { Button } from './ui/button'
import { Input } from './ui/input'

type PasswordInputProps = Omit<
  React.InputHTMLAttributes<HTMLInputElement>,
  'type'
> & {
  ref?: React.Ref<HTMLInputElement>
}

export function PasswordInput({
  className,
  disabled,
  ref,
  ...props
}: PasswordInputProps) {
  const [showPassword, setShowPassword] = React.useState(false)
  const { t } = useTranslation()

  return (
    <div className={cn('relative rounded-md', className)}>
      <Input
        type={showPassword ? 'text' : 'password'}
        ref={ref}
        disabled={disabled}
        {...props}
      />
      <Button
        type='button'
        size='icon-sm'
        variant='ghost'
        disabled={disabled}
        className='text-muted-foreground absolute end-1 top-1/2 -translate-y-1/2 active:not-aria-[haspopup]:-translate-y-1/2'
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => setShowPassword((prev) => !prev)}
        aria-label={t('Toggle password visibility')}
      >
        <Eye
          className={cn(
            'absolute size-4 transition-[opacity,transform] duration-200 motion-reduce:transition-none',
            showPassword
              ? 'scale-100 rotate-0 opacity-100'
              : 'scale-75 -rotate-12 opacity-0'
          )}
          aria-hidden='true'
        />
        <EyeOff
          className={cn(
            'absolute size-4 transition-[opacity,transform] duration-200 motion-reduce:transition-none',
            showPassword
              ? 'scale-75 rotate-12 opacity-0'
              : 'scale-100 rotate-0 opacity-100'
          )}
          aria-hidden='true'
        />
      </Button>
    </div>
  )
}
