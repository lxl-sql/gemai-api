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
  MAX_QUOTA_PER_REQUEST,
  MAX_WINDOW_QUOTA,
} from '@/lib/token-security-policy'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  API_KEY_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformSecurityPolicyToFormValues,
} from './api-key-form'

describe('transformFormDataToPayload', () => {
  it('converts displayed CNY security limits to quota units and back', () => {
    const previousConfig = useSystemConfigStore.getState().config
    useSystemConfigStore.setState({
      config: {
        ...previousConfig,
        currency: {
          ...previousConfig.currency,
          quotaDisplayType: 'CNY',
          quotaPerUnit: 500_000,
          usdExchangeRate: 7,
        },
      },
    })

    try {
      const payload = transformFormDataToPayload({
        ...API_KEY_FORM_DEFAULT_VALUES,
        max_quota_per_request: 7,
        hourly_quota: 14,
        daily_quota: 21,
        risk_mode: 'notify',
      })

      expect(payload.security_policy).toEqual({
        max_quota_per_request: 500_000,
        hourly_quota: 1_000_000,
        daily_quota: 1_500_000,
        risk_mode: 'notify',
      })
      expect(
        transformSecurityPolicyToFormValues(payload.security_policy)
      ).toEqual({
        max_quota_per_request: 7,
        hourly_quota: 14,
        daily_quota: 21,
        risk_mode: 'notify',
      })
      expect(payload.security_policy).not.toHaveProperty('sustained_rps')
      expect(payload.security_policy).not.toHaveProperty('fail_closed')

      const tinyLimitPayload = transformFormDataToPayload({
        ...API_KEY_FORM_DEFAULT_VALUES,
        max_quota_per_request: 0.000_001,
        hourly_quota: 0.000_001,
        daily_quota: 0,
      })
      expect(tinyLimitPayload.security_policy).toMatchObject({
        max_quota_per_request: 1,
        hourly_quota: 1,
        daily_quota: 0,
      })

      const maximumPolicy = {
        ...payload.security_policy,
        max_quota_per_request: MAX_QUOTA_PER_REQUEST,
        hourly_quota: MAX_WINDOW_QUOTA,
        daily_quota: MAX_WINDOW_QUOTA,
      }
      const maximumLimitPayload = transformFormDataToPayload({
        ...API_KEY_FORM_DEFAULT_VALUES,
        ...transformSecurityPolicyToFormValues(maximumPolicy),
      })
      expect(maximumLimitPayload.security_policy).toMatchObject(maximumPolicy)
    } finally {
      useSystemConfigStore.setState({ config: previousConfig })
    }
  })
})
