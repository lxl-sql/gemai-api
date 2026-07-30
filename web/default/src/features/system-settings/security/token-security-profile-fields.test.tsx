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
import { useForm } from 'react-hook-form'
import { describe, expect, test, vi } from 'vitest'

import { Form } from '@/components/ui/form'
import {
  EMPTY_TOKEN_SECURITY_PROFILE_VALUES,
  type TokenSecurityProfileValues,
} from '@/lib/token-security-profile-form'

import { TokenSecurityProfileFields } from './token-security-profile-fields'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

function ProfileFieldsHarness() {
  const form = useForm<TokenSecurityProfileValues>({
    defaultValues: EMPTY_TOKEN_SECURITY_PROFILE_VALUES,
  })

  return (
    <Form {...form}>
      <TokenSecurityProfileFields form={form} />
    </Form>
  )
}

describe('TokenSecurityProfileFields', () => {
  test('shows the localized risk label instead of the stored value', () => {
    render(<ProfileFieldsHarness />)

    expect(screen.getByText('Audit only')).toBeTruthy()
    expect(screen.queryByText('observe')).toBeNull()
  })
})
