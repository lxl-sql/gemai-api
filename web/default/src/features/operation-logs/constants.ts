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
import type { TFunction } from 'i18next'
import type { StatusVariant } from '@/components/status-badge'

/**
 * 操作大类。值必须与后端 model/operation_log.go 的 OpCategory* 常量一致。
 * label 为 i18n key（与英文字面量相同），渲染时通过 t() 翻译。
 */
export const OPERATION_LOG_CATEGORIES = [
  { value: 'auth', label: 'Authentication' },
  { value: 'user', label: 'User Management' },
  { value: 'token', label: 'API Key' },
  { value: 'finance', label: 'Finance' },
  { value: 'channel', label: 'Channel' },
  { value: 'redemption', label: 'Redemption Code' },
  { value: 'system', label: 'System' },
] as const

/**
 * 大类对应的徽章颜色。
 */
export const OPERATION_CATEGORY_VARIANTS: Record<string, StatusVariant> = {
  auth: 'blue',
  user: 'violet',
  token: 'cyan',
  finance: 'green',
  channel: 'orange',
  redemption: 'amber',
  system: 'purple',
}

/**
 * action → 展示文案（i18n key）。值必须与后端 OpAction* 常量一致。
 */
export const OPERATION_ACTION_LABELS: Record<string, string> = {
  'auth.login': 'Login',
  'auth.login_failed': 'Login Failed',
  'auth.logout': 'Logout',
  'auth.register': 'Register',
  'auth.password_reset': 'Password Reset',
  'auth.password_change': 'Change Password',
  'auth.access_token_reset': 'Reset Access Token',
  'auth.2fa_enable': 'Enable 2FA',
  'auth.2fa_disable': 'Disable 2FA',
  'auth.2fa_backup_regenerate': 'Regenerate 2FA Backup Codes',
  'auth.passkey_register': 'Register Passkey',
  'auth.passkey_delete': 'Delete Passkey',
  'auth.passkey_admin_reset': 'Reset Passkey',
  'auth.oauth_bind': 'Bind OAuth',
  'auth.oauth_unbind': 'Unbind OAuth',
  'auth.oauth_authorize': 'Authorize OAuth App',
  'auth.oauth_token_issue': 'Issue OAuth Access Token',
  'auth.oauth_grant_revoke': 'Revoke OAuth Grant',
  'auth.email_bind': 'Bind Email',
  'user.create': 'Create User',
  'user.update': 'Update User',
  'user.delete': 'Delete User',
  'user.self_update': 'Update Profile',
  'user.self_delete': 'Delete Account',
  'user.manage': 'Manage User',
  'token.create': 'Create API Key',
  'token.update': 'Update API Key',
  'token.delete': 'Delete API Key',
  'token.delete_batch': 'Batch Delete API Keys',
  'token.view_key': 'View API Key',
  'finance.topup': 'Top-up',
  'finance.redeem': 'Redeem Code',
  'finance.aff_transfer': 'Affiliate Transfer',
  'finance.admin_complete_topup': 'Admin Complete Top-up',
  'channel.create': 'Create Channel',
  'channel.update': 'Update Channel',
  'channel.delete': 'Delete Channel',
  'channel.delete_batch': 'Batch Delete Channels',
  'channel.view_key': 'View Channel Key',
  'redemption.create': 'Create Redemption Code',
  'redemption.update': 'Update Redemption Code',
  'redemption.delete': 'Delete Redemption Code',
  'system.option_update': 'Update System Settings',
  'system.model_create': 'Create Model',
  'system.model_update': 'Update Model',
  'system.model_delete': 'Delete Model',
}

/**
 * 返回 action 的 i18n key；未登记的 action 回退为原始字符串。
 */
export function getOperationActionLabelKey(action: string): string {
  return OPERATION_ACTION_LABELS[action] ?? action
}

export function getOperationCategoryLabelKey(category: string): string {
  const meta = OPERATION_LOG_CATEGORIES.find((c) => c.value === category)
  return meta?.label ?? category
}

export function getOperationCategoryOptions(t: TFunction) {
  return OPERATION_LOG_CATEGORIES.map((c) => ({
    label: t(c.label),
    value: c.value,
  }))
}

export function getSuccessFilterOptions(t: TFunction) {
  return [
    { label: t('Success'), value: '1' },
    { label: t('Failed'), value: '0' },
  ]
}
