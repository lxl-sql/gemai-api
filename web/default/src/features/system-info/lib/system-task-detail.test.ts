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

import type { SystemTask } from '@/features/system-settings/types'

import { getManualReconciliationCount } from './system-task-detail'

function task(type: string, count: unknown): SystemTask {
  return {
    id: 1,
    task_id: 'task-1',
    type,
    status: 'succeeded',
    result: { reservations_manual_required: count },
    created_at: 1,
    updated_at: 1,
  }
}

describe('getManualReconciliationCount', () => {
  it('returns the positive integer reported by billing repair', () => {
    expect(
      getManualReconciliationCount(task('billing_settlement_repair', 5))
    ).toBe(5)
  })

  it.each([0, -1, 1.5, '5', null, undefined])(
    'ignores invalid manual counts: %s',
    (count) => {
      expect(
        getManualReconciliationCount(task('billing_settlement_repair', count))
      ).toBe(0)
    }
  )

  it('ignores the same result field on another task type', () => {
    expect(getManualReconciliationCount(task('model_update', 5))).toBe(0)
  })
})
