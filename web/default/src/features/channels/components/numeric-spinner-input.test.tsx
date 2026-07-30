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
import { useState } from 'react'
import { describe, expect, test, vi } from 'vitest'

import { NumericSpinnerInput } from './numeric-spinner-input'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

function createDeferred<T>() {
  let resolve: (value: T) => void = () => {}
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function AsyncSpinner({
  result,
}: {
  result: ReturnType<typeof createDeferred<boolean>>
}) {
  const [value, setValue] = useState(0)

  return (
    <NumericSpinnerInput
      value={value}
      onChange={async (next) => {
        const saved = await result.promise
        if (saved) {
          setValue(next)
        }
        return saved
      }}
    />
  )
}

describe('NumericSpinnerInput', () => {
  test('keeps the submitted value visible while an async save is pending', async () => {
    const user = userEvent.setup()
    const result = createDeferred<boolean>()
    render(<AsyncSpinner result={result} />)

    await user.click(screen.getByRole('button', { name: '0' }))
    const input = screen.getByRole('textbox')
    await user.clear(input)
    await user.type(input, '7')
    await user.keyboard('{Enter}')

    expect(
      (screen.getByRole('button', { name: '7' }) as HTMLButtonElement).disabled
    ).toBe(true)

    result.resolve(true)
    await waitFor(() => {
      expect(
        (screen.getByRole('button', { name: '7' }) as HTMLButtonElement)
          .disabled
      ).toBe(false)
    })
  })

  test('restores the persisted value when an async save fails', async () => {
    const user = userEvent.setup()
    const result = createDeferred<boolean>()
    render(<AsyncSpinner result={result} />)

    await user.click(screen.getByRole('button', { name: '0' }))
    const input = screen.getByRole('textbox')
    await user.clear(input)
    await user.type(input, '9')
    await user.keyboard('{Enter}')
    result.resolve(false)

    expect(
      ((await screen.findByRole('button', { name: '0' })) as HTMLButtonElement)
        .disabled
    ).toBe(false)
  })

  test('keeps the last saved value when list refresh is unavailable', async () => {
    const user = userEvent.setup()
    const firstSave = createDeferred<boolean>()
    const secondSave = createDeferred<boolean>()
    const onChange = vi
      .fn()
      .mockReturnValueOnce(firstSave.promise)
      .mockReturnValueOnce(secondSave.promise)
    render(<NumericSpinnerInput value={0} onChange={onChange} />)

    await user.click(screen.getByRole('button', { name: '0' }))
    let input = screen.getByRole('textbox')
    await user.clear(input)
    await user.type(input, '7')
    await user.keyboard('{Enter}')
    firstSave.resolve(true)
    await waitFor(() => {
      expect(
        (screen.getByRole('button', { name: '7' }) as HTMLButtonElement)
          .disabled
      ).toBe(false)
    })

    await user.click(screen.getByRole('button', { name: '7' }))
    input = screen.getByRole('textbox')
    await user.clear(input)
    await user.type(input, '9')
    await user.keyboard('{Enter}')
    secondSave.resolve(false)

    expect(
      ((await screen.findByRole('button', { name: '7' })) as HTMLButtonElement)
        .disabled
    ).toBe(false)
  })

  test('restores the controlled value for synchronous confirmation callbacks', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<NumericSpinnerInput value={0} onChange={onChange} />)

    await user.click(screen.getByRole('button', { name: '0' }))
    const input = screen.getByRole('textbox')
    await user.clear(input)
    await user.type(input, '5')
    await user.keyboard('{Enter}')

    expect(onChange).toHaveBeenCalledWith(5)
    expect(
      (screen.getByRole('button', { name: '0' }) as HTMLButtonElement).disabled
    ).toBe(false)
  })
})
