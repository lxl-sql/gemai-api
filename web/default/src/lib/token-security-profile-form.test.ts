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

import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  DEFAULT_TOKEN_SECURITY_PROFILE_VALUES,
  tokenSecurityProfileToValues,
  tokenSecurityProfileValuesSchema,
  tokenSecurityProfileValuesToPayload,
} from './token-security-profile-form'

describe('token security profile quota conversion', () => {
  it('round-trips all displayed CNY limits through internal quota units', () => {
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
      const values = {
        ...DEFAULT_TOKEN_SECURITY_PROFILE_VALUES,
        max_quota_per_request: 7,
        hourly_quota: 14,
        daily_quota: 21,
        user_hourly_quota: 28,
        user_daily_quota: 35,
      }

      expect(tokenSecurityProfileValuesSchema.safeParse(values).success).toBe(
        true
      )

      const payload = tokenSecurityProfileValuesToPayload(values)
      expect(payload).toMatchObject({
        max_quota_per_request: 500_000,
        hourly_quota: 1_000_000,
        daily_quota: 1_500_000,
        user_hourly_quota: 2_000_000,
        user_daily_quota: 2_500_000,
      })

      expect(
        tokenSecurityProfileToValues({
          ...payload,
          id: 1,
          scope_type: 'platform',
          scope_value: '',
          built_in: false,
        })
      ).toEqual(values)
    } finally {
      useSystemConfigStore.setState({ config: previousConfig })
    }
  })
})
