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
import { QueryCache, QueryClient } from '@tanstack/react-query'
import { isAxiosError, isCancel } from 'axios'

import { handleServerError } from '@/lib/handle-server-error'

const MAX_QUERY_RETRIES = 2
const RETRYABLE_QUERY_STATUS_CODES = new Set([502, 503, 504])

type AppQueryClientOptions = {
  onUnauthorized: () => void
}

export function shouldRetryQuery(
  failureCount: number,
  error: unknown,
  isDevelopment: boolean
): boolean {
  if (isDevelopment || failureCount >= MAX_QUERY_RETRIES) return false
  if (!isAxiosError(error) || isCancel(error)) return false

  const status = error.response?.status
  return status === undefined || RETRYABLE_QUERY_STATUS_CODES.has(status)
}

export function createAppQueryClient(
  options: AppQueryClientOptions
): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: (failureCount, error) =>
          shouldRetryQuery(failureCount, error, import.meta.env.DEV),
        // Keep focused tabs from silently re-running heavy pages like logs.
        refetchOnWindowFocus: false,
        staleTime: 10 * 1000, // 10s
      },
      mutations: {
        onError: (error) => {
          if (!isAxiosError(error)) handleServerError(error)
        },
      },
    },
    queryCache: new QueryCache({
      onError: (error) => {
        if (isAxiosError(error) && error.response?.status === 401) {
          options.onUnauthorized()
        }
      },
    }),
  })
}
