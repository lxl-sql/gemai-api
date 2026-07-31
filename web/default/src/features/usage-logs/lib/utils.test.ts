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
import { describe, expect, test } from 'vitest'

import { buildLogStatsParams } from './utils'

describe('buildLogStatsParams', () => {
  test('keeps only dimensions supported by the aggregation tables', () => {
    const result = buildLogStatsParams({
      isAdmin: true,
      searchParams: {
        type: ['2'],
        model: 'gpt-5',
        token: 'primary',
        group: 'vip',
        channel: '7',
        username: 'alice',
        startTime: 1_720_000_000_000,
        endTime: 1_720_000_060_000,
      },
    })

    expect(result.unsupportedFilters).toEqual([])
    expect(result.params).toEqual({
      type: 2,
      model_name: 'gpt-5',
      token_name: 'primary',
      group: 'vip',
      channel: 7,
      username: 'alice',
      start_timestamp: 1_720_000_000,
      end_timestamp: 1_720_000_060,
    })
  })

  test('reports raw-log-only filters instead of silently dropping them', () => {
    const result = buildLogStatsParams({
      isAdmin: true,
      searchParams: {
        requestId: 'req-1',
        upstreamRequestId: 'upstream-1',
        requestDomain: 'api.example.com',
        requestIp: '203.0.113.10',
        userAgent: 'client',
        content: 'needle',
      },
    })

    expect(result.unsupportedFilters).toEqual([
      'request_id',
      'upstream_request_id',
      'request_domain',
      'request_ip',
      'user_agent',
      'content',
    ])
    expect(result.params).not.toHaveProperty('request_id')
  })

  test('does not send admin-only dimensions to the self statistics endpoint', () => {
    const result = buildLogStatsParams({
      isAdmin: false,
      searchParams: {
        username: 'other-user',
        channel: '9',
        model: 'claude-sonnet',
      },
    })

    expect(result.params).toMatchObject({ model_name: 'claude-sonnet' })
    expect(result.params).not.toHaveProperty('username')
    expect(result.params).not.toHaveProperty('channel')
  })
})
