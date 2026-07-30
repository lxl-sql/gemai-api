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

import { parseClientLabel } from './user-agent'

describe('parseClientLabel', () => {
  it('labels desktop Chrome on Windows', () => {
    expect(
      parseClientLabel(
        'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
      )
    ).toBe('Chrome · Windows')
  })

  it('labels Edge as Edge, not Chrome, despite sharing the Chrome token', () => {
    expect(
      parseClientLabel(
        'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36 Edg/119.0.0.0'
      )
    ).toBe('Edge · Windows')
  })

  it('labels desktop Safari on macOS', () => {
    expect(
      parseClientLabel(
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15'
      )
    ).toBe('Safari · macOS')
  })

  it('labels mobile Safari as iOS, not macOS', () => {
    expect(
      parseClientLabel(
        'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'
      )
    ).toBe('Safari · iOS')
  })

  it('labels Chrome on Android as Android, not Linux', () => {
    expect(
      parseClientLabel(
        'Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Mobile Safari/537.36'
      )
    ).toBe('Chrome · Android')
  })

  it('falls back to the leading name/version token for non-browser clients', () => {
    expect(parseClientLabel('curl/7.68.0')).toBe('curl/7.68.0')
    expect(parseClientLabel('python-requests/2.31.0')).toBe(
      'python-requests/2.31.0'
    )
    expect(parseClientLabel('PostmanRuntime/7.32.3')).toBe(
      'PostmanRuntime/7.32.3'
    )
    expect(parseClientLabel('Go-http-client/1.1')).toBe('Go-http-client/1.1')
  })

  it('falls back to the first whitespace-delimited token when there is no name/version pair', () => {
    expect(parseClientLabel('okhttp')).toBe('okhttp')
  })

  it('returns an empty string for empty input', () => {
    expect(parseClientLabel('')).toBe('')
    expect(parseClientLabel('   ')).toBe('')
  })
})
