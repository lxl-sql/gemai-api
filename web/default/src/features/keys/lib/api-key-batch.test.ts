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
import { describe, expect, it, vi } from 'vitest'

import type { ApiKeyFormData } from '../types'
import { createApiKeyBatch } from './api-key-batch'

const baseRequest: ApiKeyFormData = {
  name: 'key',
  remain_quota: 0,
  expired_time: -1,
  unlimited_quota: true,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
  group: 'default',
  cross_group_retry: false,
  security_policy: {
    max_quota_per_request: 0,
    hourly_quota: 0,
    daily_quota: 0,
    risk_mode: 'suspend',
  },
}

describe('createApiKeyBatch', () => {
  it('returns already-issued secrets when a later request fails', async () => {
    const createRequest = vi
      .fn()
      .mockResolvedValueOnce({
        success: true,
        data: { id: 1, key: 'first-secret' },
      })
      .mockResolvedValueOnce({
        success: false,
        message: 'second request failed',
      })

    const result = await createApiKeyBatch(
      [
        { ...baseRequest, name: 'first' },
        { ...baseRequest, name: 'second' },
        { ...baseRequest, name: 'third' },
      ],
      createRequest
    )

    expect(result.issuedKeys).toEqual([
      { id: 1, name: 'first', key: 'sk-first-secret' },
    ])
    expect(result.error?.message).toBe('second request failed')
    expect(createRequest).toHaveBeenCalledTimes(2)
  })

  it('leaves missing fallback copy to the localized caller', async () => {
    const createRequest = vi.fn().mockResolvedValue({
      success: false,
    })

    const result = await createApiKeyBatch([baseRequest], createRequest)

    expect(result.issuedKeys).toEqual([])
    expect(result.error?.message).toBe('')
  })

  it('preserves verification errors for the caller to retry safely', async () => {
    const verificationError = Object.assign(
      new Error('verification required'),
      {
        response: {
          status: 403,
          data: { code: 'VERIFICATION_REQUIRED' },
        },
      }
    )
    const createRequest = vi.fn().mockRejectedValue(verificationError)

    const result = await createApiKeyBatch([baseRequest], createRequest)

    expect(result.issuedKeys).toEqual([])
    expect(result.error).toBe(verificationError)
  })
})
