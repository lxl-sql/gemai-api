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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, test, vi } from 'vitest'

import type { TokenSecurityProfile } from '@/lib/token-security-policy'

import type { User } from '../../types'
import { UserTokenSecurityProfileDialog } from './user-token-security-profile-dialog'

const apiMocks = vi.hoisted(() => ({
  deleteTokenSecurityProfile: vi.fn(),
  getTokenSecurityProfiles: vi.fn(),
  updateTokenSecurityProfile: vi.fn(),
}))

vi.mock('@/features/system-settings/api', () => apiMocks)

vi.mock('@/features/auth/secure-verification', () => ({
  SecureVerificationDialog: () => null,
  useSecureVerification: () => ({
    cancel: vi.fn(),
    executeVerification: vi.fn(),
    methods: [],
    open: false,
    setCode: vi.fn(),
    state: {},
    switchMethod: vi.fn(),
    withVerification: async (action: () => Promise<unknown>) => action(),
  }),
}))

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

const user: User = {
  id: 101,
  username: 'policy-user',
  display_name: 'Policy User',
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'vip',
  status: 1,
  role: 1,
}

function profile(
  overrides: Partial<TokenSecurityProfile>
): TokenSecurityProfile {
  return {
    id: 1,
    scope_type: 'platform',
    scope_value: '',
    sustained_rps: 0,
    burst_capacity: 0,
    max_concurrency: 0,
    max_quota_per_request: 0,
    hourly_quota: 0,
    daily_quota: 0,
    max_distinct_models_5m: 0,
    minimum_risk_mode: 'observe',
    fail_closed: false,
    built_in: false,
    ...overrides,
  }
}

function renderDialog(profiles: TokenSecurityProfile[]) {
  apiMocks.getTokenSecurityProfiles.mockResolvedValue({
    success: true,
    data: profiles,
  })
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <UserTokenSecurityProfileDialog open onOpenChange={vi.fn()} user={user} />
    </QueryClientProvider>
  )
}

function inputValue(label: string): number {
  return Number((screen.getByLabelText(label) as HTMLInputElement).value)
}

async function selectCustomPolicy() {
  const interaction = userEvent.setup()
  await interaction.click(await screen.findByLabelText('Policy mode'))
  await interaction.click(
    await screen.findByRole('option', { name: 'Custom user policy' })
  )
}

describe('UserTokenSecurityProfileDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  test('uses system defaults when only the built-in fallback exists', async () => {
    renderDialog([profile({ id: 0, built_in: true })])

    await selectCustomPolicy()

    await waitFor(() => {
      expect(inputValue('Maximum sustained requests/second')).toBe(5)
    })
    expect(inputValue('Maximum burst capacity')).toBe(25)
    expect(inputValue('Maximum concurrency')).toBe(20)
    expect(inputValue('Maximum distinct models per 5 minutes')).toBe(20)
    expect(screen.getByText('Automatically suspend token')).toBeTruthy()
  })

  test('uses the matching group profile when no user override exists', async () => {
    renderDialog([
      profile({ id: 2, sustained_rps: 5, burst_capacity: 25 }),
      profile({
        id: 3,
        scope_type: 'group',
        scope_value: 'vip',
        sustained_rps: 80,
        burst_capacity: 160,
        max_concurrency: 40,
        max_distinct_models_5m: 12,
        minimum_risk_mode: 'notify',
      }),
    ])

    await selectCustomPolicy()

    await waitFor(() => {
      expect(inputValue('Maximum sustained requests/second')).toBe(80)
    })
    expect(inputValue('Maximum burst capacity')).toBe(160)
    expect(inputValue('Maximum concurrency')).toBe(40)
    expect(inputValue('Maximum distinct models per 5 minutes')).toBe(12)
    expect(screen.getByText('Audit and notify')).toBeTruthy()
  })

  test('preserves an existing user override', async () => {
    renderDialog([
      profile({
        id: 4,
        scope_type: 'user',
        scope_value: String(user.id),
        sustained_rps: 200,
        burst_capacity: 400,
        max_concurrency: 60,
        max_distinct_models_5m: 30,
        minimum_risk_mode: 'observe',
      }),
    ])

    await waitFor(() => {
      expect(inputValue('Maximum sustained requests/second')).toBe(200)
    })
    expect(inputValue('Maximum burst capacity')).toBe(400)
    expect(inputValue('Maximum concurrency')).toBe(60)
    expect(inputValue('Maximum distinct models per 5 minutes')).toBe(30)
    expect(screen.getByText('Audit only')).toBeTruthy()
  })
})
