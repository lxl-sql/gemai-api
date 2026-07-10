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

import z from 'zod'
import { createFileRoute } from '@tanstack/react-router'
import { OperationLogs } from '@/features/operation-logs'

const operationLogsSearchSchema = z.object({
  page: z.number().optional().catch(1),
  pageSize: z.number().optional().catch(undefined),
  operatorName: z.string().optional().catch(''),
  category: z.array(z.string()).optional().catch([]),
  success: z.array(z.enum(['1', '0'])).optional().catch([]),
  targetType: z.string().optional().catch(''),
  targetId: z.string().optional().catch(''),
  ip: z.string().optional().catch(''),
})

export const Route = createFileRoute('/_authenticated/operation-log/')({
  validateSearch: operationLogsSearchSchema,
  component: OperationLogRoute,
})

function OperationLogRoute() {
  return <OperationLogs search={Route.useSearch()} navigate={Route.useNavigate()} />
}
