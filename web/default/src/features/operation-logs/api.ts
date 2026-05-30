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
import type { GetOperationLogsParams, GetOperationLogsResponse } from './types'

function buildQuery(params: GetOperationLogsParams): string {
  const q = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      q.append(key, String(value))
    }
  })
  return q.toString()
}

/**
 * 拉取操作审计日志。管理员命中 `/api/operation-log/`（全部记录、支持按操作者/IP 等筛选），
 * 普通用户命中 `/api/operation-log/self`（仅本人记录）。
 */
export async function getOperationLogs(
  params: GetOperationLogsParams = {},
  isAdmin = false
): Promise<GetOperationLogsResponse> {
  const path = isAdmin ? '/api/operation-log/' : '/api/operation-log/self'
  const res = await api.get(`${path}?${buildQuery(params)}`)
  return res.data
}
