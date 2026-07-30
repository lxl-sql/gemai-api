/*
Copyright (C) 2025 QuantumNous

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

// 操作大类。value 必须与后端 model/operation_log.go 的 OpCategory* 常量一致；
// label 为 i18n 源文案（中文），渲染时通过 t() 翻译。
export const OPERATION_LOG_CATEGORIES = [
  { value: 'auth', label: '认证安全' },
  { value: 'user', label: '用户管理' },
  { value: 'token', label: '令牌' },
  { value: 'finance', label: '财务' },
  { value: 'channel', label: '渠道' },
  { value: 'redemption', label: '兑换码' },
  { value: 'system', label: '系统' },
];

// 大类对应的 Semi Tag 颜色。
export const OPERATION_CATEGORY_COLORS = {
  auth: 'blue',
  user: 'violet',
  token: 'cyan',
  finance: 'green',
  channel: 'orange',
  redemption: 'amber',
  system: 'purple',
};

export const OPERATION_TARGET_TYPE_LABELS = {
  user: '用户',
  token: '令牌',
  channel: '渠道',
  redemption: '兑换码',
  topup: '充值订单',
  checkin: '签到',
  option: '系统配置',
  model: '模型',
  oauth_app: 'OAuth 应用',
};

export const OPERATION_TARGET_TYPE_COLORS = {
  user: 'violet',
  token: 'cyan',
  channel: 'orange',
  redemption: 'amber',
  topup: 'green',
  checkin: 'green',
  option: 'purple',
  model: 'blue',
  oauth_app: 'indigo',
};

// action → 展示文案（i18n 源文案）。键必须与后端 OpAction* 常量一致。
export const OPERATION_ACTION_LABELS = {
  'auth.login': '登录',
  'auth.login_failed': '登录失败',
  'auth.logout': '退出登录',
  'auth.logout_all': '退出全部会话',
  'auth.register': '注册',
  'auth.password_reset': '重置密码',
  'auth.password_change': '修改密码',
  'auth.access_token_reset': '重置访问令牌',
  'auth.2fa_enable': '启用两步验证',
  'auth.2fa_disable': '关闭两步验证',
  'auth.2fa_backup_regenerate': '重新生成两步验证备份码',
  'auth.passkey_register': '注册通行密钥',
  'auth.passkey_delete': '删除通行密钥',
  'auth.passkey_admin_reset': '重置通行密钥',
  'auth.secure_verification': '安全验证',
  'auth.api_key_security_verification': 'API 密钥安全验证',
  'auth.oauth_bind': '绑定第三方账号',
  'auth.oauth_unbind': '解绑第三方账号',
  'auth.oauth_authorize': '授权 OAuth 应用',
  'auth.oauth_token_issue': '签发 OAuth 访问令牌',
  'auth.oauth_grant_revoke': '撤销 OAuth 授权',
  'auth.email_bind': '绑定邮箱',
  'user.create': '创建用户',
  'user.update': '更新用户',
  'user.delete': '删除用户',
  'user.self_update': '更新个人资料',
  'user.self_delete': '注销账户',
  'user.manage': '管理用户',
  'token.create': '创建令牌',
  'token.update': '更新令牌',
  'token.delete': '删除令牌',
  'token.delete_batch': '批量删除令牌',
  'token.view_key': '查看令牌密钥',
  'token.rotate': '轮换 API 密钥',
  'token.risk_detected': '检测到 API 密钥风险',
  'token.security_profile_update': '更新 API 密钥安全策略',
  'token.security_profile_delete': '删除 API 密钥安全策略',
  'finance.topup': '充值',
  'finance.redeem': '兑换码充值',
  'finance.aff_transfer': '邀请额度划转',
  'finance.admin_complete_topup': '管理员补单',
  'channel.create': '创建渠道',
  'channel.update': '更新渠道',
  'channel.delete': '删除渠道',
  'channel.delete_batch': '批量删除渠道',
  'channel.view_key': '查看渠道密钥',
  'redemption.create': '创建兑换码',
  'redemption.update': '更新兑换码',
  'redemption.delete': '删除兑换码',
  'system.option_update': '更新系统设置',
  'system.model_create': '创建模型',
  'system.model_update': '更新模型',
  'system.model_delete': '删除模型',
};

// 返回 action 的 i18n 源文案；未登记的 action 回退为原始字符串。
export function getOperationActionLabel(action) {
  return OPERATION_ACTION_LABELS[action] || action;
}

// 返回大类的 i18n 源文案；未登记的大类回退为原始字符串。
export function getOperationCategoryLabel(category) {
  const meta = OPERATION_LOG_CATEGORIES.find((c) => c.value === category);
  return meta ? meta.label : category;
}

export function getOperationTargetTypeLabel(targetType) {
  return OPERATION_TARGET_TYPE_LABELS[targetType] || targetType;
}

export function getOperationTargetTypeColor(targetType) {
  return OPERATION_TARGET_TYPE_COLORS[targetType] || 'grey';
}

// 操作者角色 → i18n 源文案，与 classic 既有角色展示保持一致。
export function getOperatorRoleLabel(role) {
  if (role >= 100) return '超级管理员';
  if (role >= 10) return '管理员';
  if (role > 0) return '普通用户';
  return '未知';
}
