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

import { isTokenUsageSourceCapabilityEnabled } from './token-usage-source-capability'

describe('isTokenUsageSourceCapabilityEnabled', () => {
  it('keeps the entry hidden for old backends without the capability field', () => {
    expect(isTokenUsageSourceCapabilityEnabled({}, false)).toBe(false)
  })

  it('does not trust a cached placeholder capability during rollback', () => {
    expect(
      isTokenUsageSourceCapabilityEnabled(
        { token_usage_source_enabled: true },
        true
      )
    ).toBe(false)
  })

  it('shows the entry only after the current backend confirms support', () => {
    expect(
      isTokenUsageSourceCapabilityEnabled(
        { token_usage_source_enabled: true },
        false
      )
    ).toBe(true)
  })
})
