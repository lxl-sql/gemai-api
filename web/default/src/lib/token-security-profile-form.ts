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

import { parseQuotaFromDollars, quotaUnitsToDollars } from './format'
import {
  DEFAULT_TOKEN_SECURITY_POLICY,
  MAX_QUOTA_PER_REQUEST,
  MAX_WINDOW_QUOTA,
  TOKEN_RISK_MODES,
  UNRESTRICTED_TOKEN_SECURITY_PROFILE,
  tokenSecurityPolicySchema,
  type TokenSecurityProfile,
} from './token-security-policy'

const displayQuotaSchema = z.number().min(0)

export const tokenSecurityProfileValuesSchema = tokenSecurityPolicySchema
  .omit({
    risk_mode: true,
    max_quota_per_request: true,
    hourly_quota: true,
    daily_quota: true,
    user_hourly_quota: true,
    user_daily_quota: true,
  })
  .extend({
    max_quota_per_request: displayQuotaSchema,
    hourly_quota: displayQuotaSchema,
    daily_quota: displayQuotaSchema,
    user_hourly_quota: displayQuotaSchema,
    user_daily_quota: displayQuotaSchema,
    minimum_risk_mode: z.enum(TOKEN_RISK_MODES),
  })

export type TokenSecurityProfileValues = z.infer<
  typeof tokenSecurityProfileValuesSchema
>

export const EMPTY_TOKEN_SECURITY_PROFILE_VALUES: TokenSecurityProfileValues = {
  sustained_rps: UNRESTRICTED_TOKEN_SECURITY_PROFILE.sustained_rps,
  sustained_rpm: UNRESTRICTED_TOKEN_SECURITY_PROFILE.sustained_rpm ?? 0,
  burst_capacity: UNRESTRICTED_TOKEN_SECURITY_PROFILE.burst_capacity,
  max_concurrency: UNRESTRICTED_TOKEN_SECURITY_PROFILE.max_concurrency,
  max_quota_per_request:
    UNRESTRICTED_TOKEN_SECURITY_PROFILE.max_quota_per_request,
  hourly_quota: UNRESTRICTED_TOKEN_SECURITY_PROFILE.hourly_quota,
  daily_quota: UNRESTRICTED_TOKEN_SECURITY_PROFILE.daily_quota,
  max_distinct_models_5m:
    UNRESTRICTED_TOKEN_SECURITY_PROFILE.max_distinct_models_5m,
  user_sustained_rpm:
    UNRESTRICTED_TOKEN_SECURITY_PROFILE.user_sustained_rpm ?? 0,
  user_burst_capacity:
    UNRESTRICTED_TOKEN_SECURITY_PROFILE.user_burst_capacity ?? 0,
  user_max_concurrency:
    UNRESTRICTED_TOKEN_SECURITY_PROFILE.user_max_concurrency ?? 0,
  user_hourly_quota: UNRESTRICTED_TOKEN_SECURITY_PROFILE.user_hourly_quota ?? 0,
  user_daily_quota: UNRESTRICTED_TOKEN_SECURITY_PROFILE.user_daily_quota ?? 0,
  minimum_risk_mode: 'observe',
  fail_closed: UNRESTRICTED_TOKEN_SECURITY_PROFILE.fail_closed,
}

export const DEFAULT_TOKEN_SECURITY_PROFILE_VALUES: TokenSecurityProfileValues =
  {
    sustained_rps: DEFAULT_TOKEN_SECURITY_POLICY.sustained_rps,
    sustained_rpm: DEFAULT_TOKEN_SECURITY_POLICY.sustained_rpm ?? 0,
    burst_capacity: DEFAULT_TOKEN_SECURITY_POLICY.burst_capacity,
    max_concurrency: DEFAULT_TOKEN_SECURITY_POLICY.max_concurrency,
    max_quota_per_request: DEFAULT_TOKEN_SECURITY_POLICY.max_quota_per_request,
    hourly_quota: DEFAULT_TOKEN_SECURITY_POLICY.hourly_quota,
    daily_quota: DEFAULT_TOKEN_SECURITY_POLICY.daily_quota,
    max_distinct_models_5m:
      DEFAULT_TOKEN_SECURITY_POLICY.max_distinct_models_5m,
    user_sustained_rpm: DEFAULT_TOKEN_SECURITY_POLICY.user_sustained_rpm ?? 0,
    user_burst_capacity: DEFAULT_TOKEN_SECURITY_POLICY.user_burst_capacity ?? 0,
    user_max_concurrency:
      DEFAULT_TOKEN_SECURITY_POLICY.user_max_concurrency ?? 0,
    user_hourly_quota: DEFAULT_TOKEN_SECURITY_POLICY.user_hourly_quota ?? 0,
    user_daily_quota: DEFAULT_TOKEN_SECURITY_POLICY.user_daily_quota ?? 0,
    minimum_risk_mode: DEFAULT_TOKEN_SECURITY_POLICY.risk_mode,
    fail_closed: DEFAULT_TOKEN_SECURITY_POLICY.fail_closed,
  }

export function tokenSecurityProfileToValues(
  profile: TokenSecurityProfile
): TokenSecurityProfileValues {
  return {
    sustained_rps: profile.sustained_rps,
    sustained_rpm: profile.sustained_rpm ?? 0,
    burst_capacity: profile.burst_capacity,
    max_concurrency: profile.max_concurrency,
    max_quota_per_request: quotaUnitsToDollars(profile.max_quota_per_request),
    hourly_quota: quotaUnitsToDollars(profile.hourly_quota),
    daily_quota: quotaUnitsToDollars(profile.daily_quota),
    max_distinct_models_5m: profile.max_distinct_models_5m,
    user_sustained_rpm: profile.user_sustained_rpm ?? 0,
    user_burst_capacity: profile.user_burst_capacity ?? 0,
    user_max_concurrency: profile.user_max_concurrency ?? 0,
    user_hourly_quota: quotaUnitsToDollars(profile.user_hourly_quota ?? 0),
    user_daily_quota: quotaUnitsToDollars(profile.user_daily_quota ?? 0),
    minimum_risk_mode: profile.minimum_risk_mode,
    fail_closed: profile.fail_closed,
  }
}

function parseSecurityQuotaFromDisplay(
  amount: number,
  maximum: number
): number {
  const quota = parseQuotaFromDollars(amount)
  return amount > 0 ? Math.min(maximum, Math.max(1, quota)) : quota
}

export function tokenSecurityProfileValuesToPayload(
  values: TokenSecurityProfileValues
) {
  return {
    ...values,
    max_quota_per_request: parseSecurityQuotaFromDisplay(
      values.max_quota_per_request,
      MAX_QUOTA_PER_REQUEST
    ),
    hourly_quota: parseSecurityQuotaFromDisplay(
      values.hourly_quota,
      MAX_WINDOW_QUOTA
    ),
    daily_quota: parseSecurityQuotaFromDisplay(
      values.daily_quota,
      MAX_WINDOW_QUOTA
    ),
    user_hourly_quota: parseSecurityQuotaFromDisplay(
      values.user_hourly_quota,
      MAX_WINDOW_QUOTA
    ),
    user_daily_quota: parseSecurityQuotaFromDisplay(
      values.user_daily_quota,
      MAX_WINDOW_QUOTA
    ),
  }
}
