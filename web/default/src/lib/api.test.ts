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
import { CanceledError } from 'axios'
import { toast } from 'sonner'
import { describe, expect, test, vi } from 'vitest'

import { api } from './api'

vi.mock('sonner', () => ({
  toast: { error: vi.fn() },
}))

describe('API error handling', () => {
  test('does not show an error toast for a canceled request', async () => {
    await expect(
      api.get('/canceled-request', {
        adapter: () => Promise.reject(new CanceledError()),
        disableDuplicate: true,
      })
    ).rejects.toBeInstanceOf(CanceledError)

    expect(toast.error).not.toHaveBeenCalled()
  })
})
