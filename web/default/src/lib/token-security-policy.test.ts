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
import { describe, expect, it } from 'vitest'

import {
  DEFAULT_TOKEN_SECURITY_POLICY,
  MAX_QUOTA_PER_REQUEST,
  mergeTokenSecurityPolicy,
  tokenSecurityPolicySchema,
  type TokenSecurityPolicy,
  type TokenSecurityProfile,
} from './token-security-policy'

describe('DEFAULT_TOKEN_SECURITY_POLICY', () => {
  it('keeps new API keys unrestricted until an administrator profile applies', () => {
    expect(DEFAULT_TOKEN_SECURITY_POLICY).toEqual({
      sustained_rps: 0,
      burst_capacity: 0,
      max_concurrency: 0,
      max_quota_per_request: 0,
      hourly_quota: 0,
      daily_quota: 0,
      max_distinct_models_5m: 0,
      risk_mode: 'observe',
      fail_closed: false,
    })
  })
})

describe('mergeTokenSecurityPolicy', () => {
  it('uses administrator capacity and lets the token tighten budgets', () => {
    const requested: TokenSecurityPolicy = {
      sustained_rps: 0,
      burst_capacity: 0,
      max_concurrency: 80,
      max_quota_per_request: 0,
      hourly_quota: 200,
      daily_quota: 0,
      max_distinct_models_5m: 50,
      risk_mode: 'observe',
      fail_closed: true,
    }
    const profile: TokenSecurityProfile = {
      id: 1,
      scope_type: 'group',
      scope_value: 'vip',
      sustained_rps: 100,
      burst_capacity: 500,
      max_concurrency: 40,
      max_quota_per_request: 1000,
      hourly_quota: 5000,
      daily_quota: 20000,
      max_distinct_models_5m: 20,
      minimum_risk_mode: 'notify',
      fail_closed: false,
      built_in: false,
    }

    expect(mergeTokenSecurityPolicy(requested, profile)).toEqual({
      token_id: undefined,
      sustained_rps: 100,
      burst_capacity: 500,
      max_concurrency: 40,
      max_quota_per_request: 1000,
      hourly_quota: 200,
      daily_quota: 20000,
      max_distinct_models_5m: 20,
      risk_mode: 'notify',
      fail_closed: false,
    })
  })

  it('uses an enterprise administrator capacity without the token default capping it', () => {
    const requested: TokenSecurityPolicy = {
      sustained_rps: 5,
      burst_capacity: 25,
      max_concurrency: 20,
      max_quota_per_request: 0,
      hourly_quota: 0,
      daily_quota: 0,
      max_distinct_models_5m: 20,
      risk_mode: 'suspend',
      fail_closed: false,
    }
    const profile: TokenSecurityProfile = {
      id: 2,
      scope_type: 'user',
      scope_value: '1001',
      sustained_rps: 5000,
      burst_capacity: 10000,
      max_concurrency: 2000,
      max_quota_per_request: 0,
      hourly_quota: 0,
      daily_quota: 0,
      max_distinct_models_5m: 100,
      minimum_risk_mode: 'observe',
      fail_closed: true,
      built_in: false,
    }

    const effective = mergeTokenSecurityPolicy(requested, profile)

    expect(effective.sustained_rps).toBe(5000)
    expect(effective.burst_capacity).toBe(10000)
    expect(effective.max_concurrency).toBe(2000)
    expect(effective.fail_closed).toBe(true)
  })

  it('preserves legacy capacity while only the built-in profile applies', () => {
    const requested: TokenSecurityPolicy = {
      sustained_rps: 120,
      burst_capacity: 600,
      max_concurrency: 80,
      max_quota_per_request: 0,
      hourly_quota: 0,
      daily_quota: 0,
      max_distinct_models_5m: 40,
      risk_mode: 'notify',
      fail_closed: true,
    }
    const profile: TokenSecurityProfile = {
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
    }

    expect(mergeTokenSecurityPolicy(requested, profile)).toEqual(requested)
  })

  it('rejects quota values that the backend cannot represent safely', () => {
    const policy = {
      sustained_rps: 0,
      burst_capacity: 0,
      max_concurrency: 0,
      max_quota_per_request: MAX_QUOTA_PER_REQUEST + 1,
      hourly_quota: 0,
      daily_quota: 0,
      max_distinct_models_5m: 0,
      risk_mode: 'observe' as const,
      fail_closed: false,
    }

    expect(tokenSecurityPolicySchema.safeParse(policy).success).toBe(false)
  })
})
