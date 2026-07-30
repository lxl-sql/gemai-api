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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { describe, expect, test, vi } from 'vitest'

import { SecureVerificationDialog } from './secure-verification-dialog'

type MockDialogProps = {
  title: ReactNode
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
}

vi.mock('@/components/dialog', () => ({
  Dialog: (props: MockDialogProps) => (
    <div>
      <div>{props.title}</div>
      <div>{props.description}</div>
      <div>{props.children}</div>
      <div>{props.footer}</div>
    </div>
  ),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

const unavailableMethods = {
  hasPassword: false,
  has2FA: false,
  hasPasskey: false,
  passkeySupported: false,
}

test('uses translated fallback copy for unavailable verification', () => {
  render(
    <SecureVerificationDialog
      open
      onOpenChange={vi.fn()}
      methods={unavailableMethods}
      state={{ method: null, loading: false, code: '' }}
      onVerify={vi.fn()}
      onCancel={vi.fn()}
      onCodeChange={vi.fn()}
      onMethodChange={vi.fn()}
    />
  )

  expect(screen.getByText('Verification unavailable')).toBeTruthy()
  expect(
    screen.getAllByText(
      'Set a password, Two-factor Authentication, or Passkey before proceeding'
    )
  ).toHaveLength(2)
})

describe('verification submission', () => {
  test('contains a rejected verification promise after the flow reports it', async () => {
    const onVerify = vi.fn().mockRejectedValue(new Error('Invalid password'))

    render(
      <SecureVerificationDialog
        open
        onOpenChange={vi.fn()}
        methods={{ ...unavailableMethods, hasPassword: true }}
        state={{ method: 'password', loading: false, code: 'secret' }}
        onVerify={onVerify}
        onCancel={vi.fn()}
        onCodeChange={vi.fn()}
        onMethodChange={vi.fn()}
      />
    )

    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))

    await waitFor(() => {
      expect(onVerify).toHaveBeenCalledWith('password', 'secret')
    })
  })
})
