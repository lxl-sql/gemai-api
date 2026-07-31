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
import type { ReactNode } from 'react'
import { expect, test, vi } from 'vitest'

import { ApiKeyUsageSourcesDialog } from './api-key-usage-sources-dialog'

const { useQueryMock } = vi.hoisted(() => ({
  useQueryMock: vi.fn(),
}))

vi.mock('@/components/dialog', () => ({
  Dialog: (props: { children: ReactNode }) => <div>{props.children}</div>,
}))

vi.mock('@tanstack/react-query', () => ({
  useQuery: (options: unknown) => {
    useQueryMock(options)
    return {
      data: {
        items: [
          {
            ip: '192.0.2.1',
            user_agent: '',
            first_seen_at: 0,
            last_seen_at: 0,
            last_success_at: 0,
            last_error_at: 0,
            request_count: 12345,
            success_count: 12000,
            error_count: 345,
          },
        ],
        total: 1,
        page: 1,
        page_size: 50,
        tracking_enabled: true,
        tracking_start: 0,
        truncated: false,
        available: true,
        counting_mode: 'direct_batch',
        update_interval_seconds: 5,
      },
      isLoading: false,
      isError: false,
      isFetching: false,
    }
  },
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
    i18n: {
      language: 'zhCN',
      resolvedLanguage: 'zhCN',
    },
  }),
}))

vi.mock('./api-keys-provider', () => ({
  useApiKeys: () => ({
    open: 'usage-sources',
    setOpen: vi.fn(),
    currentRow: {
      id: 1,
      name: 'test-key',
    },
  }),
}))

test('formats request counts when i18next resolves simplified Chinese', () => {
  render(<ApiKeyUsageSourcesDialog />)

  expect(useQueryMock).toHaveBeenCalledWith(
    expect.objectContaining({ refetchInterval: 5_000 })
  )
  expect(screen.getByText('12,345 total')).toBeTruthy()
  expect(screen.getByText('12,000 succeeded · 345 failed')).toBeTruthy()
  expect(screen.getByText('Direct request counting')).toBeTruthy()
  expect(screen.getByText('New requests update within 5 seconds')).toBeTruthy()
})
