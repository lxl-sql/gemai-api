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
import { api } from '@/lib/api'
import type { TokenSecurityPolicyView } from '@/lib/token-security-policy'

import type {
  ApiKey,
  ApiResponse,
  GetApiKeysParams,
  GetApiKeysResponse,
  SearchApiKeysParams,
  ApiKeyFormData,
  IssuedApiKey,
  TokenUsageSourcesPage,
  TokenSecurityPolicy,
  UserTokenSecurityPolicy,
} from './types'

// ============================================================================
// API Key Management
// ============================================================================

// Get paginated API keys list
export async function getApiKeys(
  params: GetApiKeysParams = {}
): Promise<GetApiKeysResponse> {
  const { p = 1, size = 10 } = params
  const res = await api.get(`/api/token/?p=${p}&size=${size}`)
  return res.data
}

// Search API keys by keyword or token (with pagination)
export async function searchApiKeys(
  params: SearchApiKeysParams
): Promise<GetApiKeysResponse> {
  const { keyword = '', token = '', p, size } = params
  const queryParams = new URLSearchParams()
  if (keyword) queryParams.set('keyword', keyword)
  if (token) queryParams.set('token', token)
  if (p != null) queryParams.set('p', String(p))
  if (size != null) queryParams.set('size', String(size))
  const res = await api.get(`/api/token/search?${queryParams.toString()}`)
  return res.data
}

// Get single API key by ID
export async function getApiKey(id: number): Promise<ApiResponse<ApiKey>> {
  const res = await api.get(`/api/token/${id}`)
  return res.data
}

// Create a new API key
export async function createApiKey(
  data: ApiKeyFormData
): Promise<ApiResponse<IssuedApiKey>> {
  const res = await api.post('/api/token/', data)
  return res.data
}

export async function rotateApiKey(
  id: number
): Promise<ApiResponse<IssuedApiKey>> {
  const res = await api.post(`/api/token/${id}/rotate`)
  return res.data
}

export async function getTokenUsageSources(
  id: number,
  page = 1,
  pageSize = 50
): Promise<ApiResponse<TokenUsageSourcesPage>> {
  const res = await api.get(
    `/api/token/${id}/usage-sources?p=${page}&page_size=${pageSize}`
  )
  return res.data
}

// Update an existing API key
export async function updateApiKey(
  data: ApiKeyFormData & { id: number }
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put('/api/token/', data)
  return res.data
}

// Delete a single API key
export async function deleteApiKey(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/token/${id}/`)
  return res.data
}

// Batch delete multiple API keys
export async function batchDeleteApiKeys(
  ids: number[]
): Promise<ApiResponse<number>> {
  const res = await api.post('/api/token/batch', { ids })
  return res.data
}

// Update API key status (enable/disable)
export async function updateApiKeyStatus(
  id: number,
  status: number
): Promise<ApiResponse<ApiKey>> {
  const res = await api.put('/api/token/?status_only=true', { id, status })
  return res.data
}

export async function getTokenSecurityPolicy(
  id: number
): Promise<ApiResponse<TokenSecurityPolicyView>> {
  const res = await api.get(`/api/token/${id}/security-policy`)
  return res.data
}

export async function getDefaultTokenSecurityPolicy(): Promise<
  ApiResponse<TokenSecurityPolicyView>
> {
  const res = await api.get('/api/token/security-policy-default')
  return res.data
}

export async function updateTokenSecurityPolicy(
  id: number,
  policy: UserTokenSecurityPolicy
): Promise<ApiResponse<TokenSecurityPolicy>> {
  const res = await api.put(`/api/token/${id}/security-policy`, policy)
  return res.data
}
