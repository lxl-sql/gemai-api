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
import { AxiosError, CanceledError } from 'axios'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { handleServerError } from '@/lib/handle-server-error'

import { createAppQueryClient, shouldRetryQuery } from './query-client'

vi.mock('@/lib/handle-server-error', () => ({
  handleServerError: vi.fn(),
}))

const handleServerErrorMock = vi.mocked(handleServerError)

function httpError(status?: number): AxiosError {
  const error = new AxiosError('request failed')
  if (status !== undefined) {
    Object.defineProperty(error, 'response', {
      value: { status },
    })
  }
  return error
}

const queryClients: ReturnType<typeof createAppQueryClient>[] = []

afterEach(() => {
  for (const queryClient of queryClients) queryClient.clear()
  queryClients.length = 0
  handleServerErrorMock.mockClear()
})

describe('application query client', () => {
  test('retries only transient network and gateway failures', () => {
    expect(shouldRetryQuery(0, httpError(), false)).toBe(true)
    expect(shouldRetryQuery(1, httpError(502), false)).toBe(true)
    expect(shouldRetryQuery(0, httpError(503), false)).toBe(true)
    expect(shouldRetryQuery(0, httpError(504), false)).toBe(true)

    expect(shouldRetryQuery(0, httpError(500), false)).toBe(false)
    expect(shouldRetryQuery(0, httpError(401), false)).toBe(false)
    expect(shouldRetryQuery(0, new CanceledError(), false)).toBe(false)
    expect(shouldRetryQuery(2, httpError(503), false)).toBe(false)
    expect(shouldRetryQuery(0, new Error('parse failed'), false)).toBe(false)
    expect(shouldRetryQuery(0, httpError(503), true)).toBe(false)
  })

  test('keeps server errors on the current page', async () => {
    const onUnauthorized = vi.fn()
    const queryClient = createAppQueryClient({ onUnauthorized })
    queryClients.push(queryClient)

    await expect(
      queryClient.fetchQuery({
        queryKey: ['server-error'],
        queryFn: () => Promise.reject(httpError(500)),
        retry: false,
      })
    ).rejects.toBeInstanceOf(AxiosError)

    expect(onUnauthorized).not.toHaveBeenCalled()
  })

  test('delegates unauthorized queries to the session handler', async () => {
    const onUnauthorized = vi.fn()
    const queryClient = createAppQueryClient({ onUnauthorized })
    queryClients.push(queryClient)

    await expect(
      queryClient.fetchQuery({
        queryKey: ['unauthorized'],
        queryFn: () => Promise.reject(httpError(401)),
        retry: false,
      })
    ).rejects.toBeInstanceOf(AxiosError)

    expect(onUnauthorized).toHaveBeenCalledOnce()
  })

  test('leaves Axios mutation errors to the API interceptor', () => {
    const queryClient = createAppQueryClient({ onUnauthorized: vi.fn() })
    queryClients.push(queryClient)
    const onMutationError = queryClient.getDefaultOptions().mutations
      ?.onError as ((error: unknown) => void) | undefined

    expect(onMutationError).toBeDefined()

    onMutationError?.(httpError(500))
    expect(handleServerErrorMock).not.toHaveBeenCalled()

    onMutationError?.(new Error('local mutation failed'))
    expect(handleServerErrorMock).toHaveBeenCalledOnce()
  })
})
