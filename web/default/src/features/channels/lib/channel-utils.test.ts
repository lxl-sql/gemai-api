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

import type { Channel } from '../types'
import { getChannelRowId, type TagRow } from './channel-utils'

describe('getChannelRowId', () => {
  test('keeps regular channel identity stable across priority reordering', () => {
    expect(getChannelRowId({ id: 42 } as Channel)).toBe('channel:42')
  })

  test('keeps tag rows distinct from numeric channel IDs', () => {
    const tagRow = {
      id: '42',
      tag: '42',
      children: [],
    } as unknown as TagRow

    expect(getChannelRowId(tagRow)).toBe('tag:42')
  })
})
