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
import { describe, expect, test, vi } from 'vitest'

import type { TokenSecurityPolicyView } from '@/lib/token-security-policy'

import { AdministratorTokenSecuritySummary } from './api-key-security-policy-summary'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
  }),
}))

const unrestrictedView: TokenSecurityPolicyView = {
  token_id: 1,
  sustained_rps: 0,
  burst_capacity: 0,
  max_concurrency: 0,
  max_quota_per_request: 0,
  hourly_quota: 0,
  daily_quota: 0,
  max_distinct_models_5m: 0,
  risk_mode: 'observe',
  fail_closed: false,
  admin_profile: {
    id: 0,
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
    built_in: true,
  },
  effective_policy: {
    sustained_rps: 0,
    burst_capacity: 0,
    max_concurrency: 0,
    max_quota_per_request: 0,
    hourly_quota: 0,
    daily_quota: 0,
    max_distinct_models_5m: 0,
    risk_mode: 'observe',
    fail_closed: false,
  },
}

describe('AdministratorTokenSecuritySummary', () => {
  test('identifies the unrestricted built-in fallback', () => {
    render(<AdministratorTokenSecuritySummary view={unrestrictedView} />)

    expect(screen.getByText('Built-in fallback')).toBeTruthy()
    expect(
      screen.getByText(
        'No administrator traffic policy is configured. Capacity remains unrestricted.'
      )
    ).toBeTruthy()
    expect(screen.queryByText('Administrator policy')).toBeNull()
  })

  test('shows values from a persisted administrator profile', () => {
    const view: TokenSecurityPolicyView = {
      ...unrestrictedView,
      admin_profile: {
        ...unrestrictedView.admin_profile,
        id: 2,
        sustained_rps: 100,
        burst_capacity: 500,
        max_concurrency: 80,
        max_distinct_models_5m: 40,
        built_in: false,
      },
      effective_policy: {
        ...unrestrictedView.effective_policy,
        sustained_rps: 1,
        burst_capacity: 5,
        max_concurrency: 3,
        max_distinct_models_5m: 2,
      },
    }

    render(<AdministratorTokenSecuritySummary view={view} />)

    expect(screen.getByText('Administrator policy')).toBeTruthy()
    expect(screen.getByText('100')).toBeTruthy()
    expect(screen.getByText('500')).toBeTruthy()
    expect(screen.getByText('80')).toBeTruthy()
    expect(screen.getByText('40')).toBeTruthy()
    expect(screen.queryByText('Built-in fallback')).toBeNull()
  })
})
