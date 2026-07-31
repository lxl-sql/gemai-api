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
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { ModelLimitsPicker } from './model-limits-picker'

const PRICING = {
  success: true,
  data: [
    { model_name: 'gpt-5.1', vendor_id: 1, quota_type: 0 },
    { model_name: 'gpt-image-1', vendor_id: 1, quota_type: 1 },
    { model_name: 'claude-opus-4-6', vendor_id: 2, quota_type: 0 },
  ],
  vendors: [
    { id: 1, name: 'OpenAI' },
    { id: 2, name: 'Anthropic' },
  ],
}

vi.mock('@tanstack/react-query', () => ({
  useQuery: () => ({ data: PRICING }),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, values?: Record<string, unknown>) => {
      if (!values) return key
      return Object.entries(values).reduce(
        (text, [name, value]) => text.replaceAll(`{{${name}}}`, String(value)),
        key
      )
    },
  }),
}))

// `local-only` has no pricing entry, so it lands in the unknown-vendor bucket.
const MODELS = ['gpt-5.1', 'gpt-image-1', 'claude-opus-4-6', 'local-only']

function renderPicker(selected: string[] = []) {
  const onChange = vi.fn()
  const view = render(
    <ModelLimitsPicker
      models={MODELS}
      value={selected}
      onChange={onChange}
      disabled={false}
    />
  )
  return { onChange, view }
}

async function expandPanel(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    screen.getByRole('button', {
      name: /Select models \(empty for allow all\)|models selected/,
    })
  )
}

function listedModels(): string[] {
  return [...MODELS, 'retired-model'].filter(
    (name) => screen.queryAllByText(name).length > 0
  )
}

function filterChips(rowLabel: string): string[] {
  const row = screen.getByText(rowLabel).parentElement
  if (!row) throw new Error(`filter row not found: ${rowLabel}`)
  return within(row)
    .getAllByRole('button')
    .map((button) => button.textContent ?? '')
}

beforeEach(() => {
  vi.clearAllMocks()
})

test('vendor filter narrows the list to that vendor', async () => {
  const user = userEvent.setup()
  renderPicker()
  await expandPanel(user)

  expect(listedModels()).toEqual(MODELS)

  await user.click(screen.getByRole('button', { name: /OpenAI/ }))

  expect(listedModels()).toEqual(['gpt-5.1', 'gpt-image-1'])
})

test('billing filter keeps only models with the matching quota type', async () => {
  const user = userEvent.setup()
  renderPicker()
  await expandPanel(user)

  await user.click(screen.getByRole('button', { name: /Per Request/ }))

  expect(listedModels()).toEqual(['gpt-image-1'])
})

test('search matches model name and vendor name', async () => {
  const user = userEvent.setup()
  renderPicker()
  await expandPanel(user)

  await user.type(screen.getByPlaceholderText('Search models...'), 'anthropic')

  expect(listedModels()).toEqual(['claude-opus-4-6'])
})

test('select all adds only the filtered models to the existing selection', async () => {
  const user = userEvent.setup()
  const { onChange } = renderPicker(['local-only'])
  await expandPanel(user)

  await user.click(screen.getByRole('button', { name: /OpenAI/ }))
  await user.click(
    screen.getByRole('checkbox', { name: 'Select all (filtered)' })
  )

  expect(onChange).toHaveBeenCalledWith([
    'local-only',
    'gpt-5.1',
    'gpt-image-1',
  ])
})

test('clearing select all drops only the filtered models', async () => {
  const user = userEvent.setup()
  const { onChange } = renderPicker(['local-only', 'gpt-5.1', 'gpt-image-1'])
  await expandPanel(user)

  await user.click(screen.getByRole('button', { name: /OpenAI/ }))
  await user.click(
    screen.getByRole('checkbox', { name: 'Select all (filtered)' })
  )

  expect(onChange).toHaveBeenCalledWith(['local-only'])
})

test('filter counts are computed against the other active filter', async () => {
  const user = userEvent.setup()
  renderPicker()
  await expandPanel(user)

  expect(filterChips('Billing')).toEqual([
    'All4',
    'Per Request1',
    'Token-based2',
  ])

  await user.click(screen.getByRole('button', { name: /OpenAI/ }))

  expect(filterChips('Billing')).toEqual([
    'All2',
    'Per Request1',
    'Token-based1',
  ])
})

test('vendor chips drop to the models the billing filter can still produce', async () => {
  const user = userEvent.setup()
  renderPicker()
  await expandPanel(user)

  await user.click(screen.getByRole('button', { name: /Per Request/ }))

  expect(filterChips('Vendor')).toEqual(['All1', 'OpenAI1'])
})

test('an active vendor stays listed at zero so it can be undone', async () => {
  const user = userEvent.setup()
  renderPicker()
  await expandPanel(user)

  await user.click(screen.getByRole('button', { name: /Anthropic/ }))
  await user.click(screen.getByRole('button', { name: /Per Request/ }))

  // Vendor counts stay relative to the billing filter; the now-empty active
  // vendor is pinned at zero so the user can click their way back out.
  expect(filterChips('Vendor')).toEqual(['All1', 'OpenAI1', 'Anthropic0'])
  expect(screen.getByText('No matching models')).toBeTruthy()
})

test('models kept on the key but no longer available stay selectable', async () => {
  const user = userEvent.setup()
  const { onChange } = renderPicker(['retired-model'])
  await expandPanel(user)

  expect(listedModels()).toContain('retired-model')

  await user.click(screen.getByRole('checkbox', { name: /retired-model/ }))

  expect(onChange).toHaveBeenCalledWith([])
})
