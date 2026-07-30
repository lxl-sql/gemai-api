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
import { createApiKey } from '../api'
import type {
  ApiKeyFormData,
  ApiResponse,
  IssuedApiKey,
  OneTimeApiKeySecret,
} from '../types'

type CreateApiKeyRequest = (
  data: ApiKeyFormData
) => Promise<ApiResponse<IssuedApiKey>>

export type ApiKeyBatchResult = {
  issuedKeys: OneTimeApiKeySecret[]
  error?: Error
}

export async function createApiKeyBatch(
  requests: ApiKeyFormData[],
  createRequest: CreateApiKeyRequest = createApiKey
): Promise<ApiKeyBatchResult> {
  const issuedKeys: OneTimeApiKeySecret[] = []

  for (const request of requests) {
    try {
      const result = await createRequest(request)
      if (!result.success || !result.data?.key) {
        return {
          issuedKeys,
          error: new Error(result.message || ''),
        }
      }
      issuedKeys.push({
        id: result.data.id,
        name: request.name,
        key: `sk-${result.data.key}`,
      })
    } catch (error) {
      return {
        issuedKeys,
        error: error instanceof Error ? error : new Error(''),
      }
    }
  }

  return { issuedKeys }
}
