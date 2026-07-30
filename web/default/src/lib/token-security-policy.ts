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
import { z } from 'zod'

export const TOKEN_RISK_MODES = ['observe', 'notify', 'suspend'] as const
export const MAX_QUOTA_PER_REQUEST = 2_147_483_647
export const MAX_WINDOW_QUOTA = Number.MAX_SAFE_INTEGER

export type TokenRiskMode = (typeof TOKEN_RISK_MODES)[number]

export const tokenSecurityPolicySchema = z.object({
  sustained_rps: z.number().int().min(0).max(100000),
  burst_capacity: z.number().int().min(0).max(1000000),
  max_concurrency: z.number().int().min(0).max(1000000),
  max_quota_per_request: z.number().int().min(0).max(MAX_QUOTA_PER_REQUEST),
  hourly_quota: z.number().int().min(0).max(MAX_WINDOW_QUOTA),
  daily_quota: z.number().int().min(0).max(MAX_WINDOW_QUOTA),
  max_distinct_models_5m: z.number().int().min(0).max(10000),
  risk_mode: z.enum(TOKEN_RISK_MODES),
  fail_closed: z.boolean(),
})

export type TokenSecurityPolicy = z.infer<typeof tokenSecurityPolicySchema> & {
  token_id?: number
}

export const userTokenSecurityPolicySchema = tokenSecurityPolicySchema.pick({
  max_quota_per_request: true,
  hourly_quota: true,
  daily_quota: true,
  risk_mode: true,
})

export type UserTokenSecurityPolicy = z.infer<
  typeof userTokenSecurityPolicySchema
>

export type TokenSecurityProfile = {
  id: number
  scope_type: 'platform' | 'group' | 'user'
  scope_value: string
  sustained_rps: number
  burst_capacity: number
  max_concurrency: number
  max_quota_per_request: number
  hourly_quota: number
  daily_quota: number
  max_distinct_models_5m: number
  minimum_risk_mode: TokenRiskMode
  fail_closed: boolean
  built_in: boolean
}

export type TokenSecurityPolicyView = TokenSecurityPolicy & {
  admin_profile: TokenSecurityProfile
  effective_policy: TokenSecurityPolicy
}

export const DEFAULT_TOKEN_SECURITY_POLICY: TokenSecurityPolicy = {
  sustained_rps: 0,
  burst_capacity: 0,
  max_concurrency: 0,
  max_quota_per_request: 0,
  hourly_quota: 0,
  daily_quota: 0,
  max_distinct_models_5m: 0,
  risk_mode: 'observe',
  fail_closed: false,
}

export const DEFAULT_USER_TOKEN_SECURITY_POLICY: UserTokenSecurityPolicy = {
  max_quota_per_request: DEFAULT_TOKEN_SECURITY_POLICY.max_quota_per_request,
  hourly_quota: DEFAULT_TOKEN_SECURITY_POLICY.hourly_quota,
  daily_quota: DEFAULT_TOKEN_SECURITY_POLICY.daily_quota,
  risk_mode: DEFAULT_TOKEN_SECURITY_POLICY.risk_mode,
}

export const UNRESTRICTED_TOKEN_SECURITY_PROFILE: Omit<
  TokenSecurityProfile,
  'id'
> = {
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

export function tokenRiskModeRank(mode: TokenRiskMode): number {
  return TOKEN_RISK_MODES.indexOf(mode)
}

function stricterLimit(
  requested: number,
  administratorMaximum: number
): number {
  if (requested <= 0) return administratorMaximum
  if (administratorMaximum <= 0) return requested
  return Math.min(requested, administratorMaximum)
}

export function mergeTokenSecurityPolicy(
  requested: TokenSecurityPolicy,
  profile: TokenSecurityProfile
): TokenSecurityPolicy {
  const effective: TokenSecurityPolicy = {
    token_id: requested.token_id,
    sustained_rps: profile.built_in
      ? requested.sustained_rps
      : profile.sustained_rps,
    burst_capacity: profile.built_in
      ? requested.burst_capacity
      : profile.burst_capacity,
    max_concurrency: profile.built_in
      ? requested.max_concurrency
      : profile.max_concurrency,
    max_quota_per_request: stricterLimit(
      requested.max_quota_per_request,
      profile.max_quota_per_request
    ),
    hourly_quota: stricterLimit(requested.hourly_quota, profile.hourly_quota),
    daily_quota: stricterLimit(requested.daily_quota, profile.daily_quota),
    max_distinct_models_5m: profile.built_in
      ? requested.max_distinct_models_5m
      : profile.max_distinct_models_5m,
    risk_mode:
      tokenRiskModeRank(requested.risk_mode) <
      tokenRiskModeRank(profile.minimum_risk_mode)
        ? profile.minimum_risk_mode
        : requested.risk_mode,
    fail_closed: profile.built_in ? requested.fail_closed : profile.fail_closed,
  }
  if (effective.sustained_rps === 0) {
    effective.burst_capacity = 0
  } else if (effective.burst_capacity < effective.sustained_rps) {
    effective.burst_capacity = effective.sustained_rps
  }
  return effective
}
