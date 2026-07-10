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

export const OPERATION_TARGET_TYPE_LABELS: Record<string, string> = {
  user: 'User',
  token: 'API Key',
  channel: 'Channel',
  redemption: 'Redemption Code',
  option: 'System Option',
  topup: 'Top-up Order',
  checkin: 'Check-in',
  oauth_app: 'OAuth App',
  model: 'Model',
}

export const OPERATION_TARGET_TYPE_VARIANTS: Record<string, StatusVariant> = {
  user: 'violet',
  token: 'cyan',
  channel: 'orange',
  redemption: 'amber',
  option: 'purple',
  topup: 'green',
  checkin: 'green',
  oauth_app: 'blue',
  model: 'neutral',
}

/**
 * System option keys are persisted as stable backend identifiers in operation
 * logs. Keep the log data immutable and translate the known identifiers only
 * at render time.
 */
export const OPERATION_OPTION_LABELS: Record<string, string> = {
  About: 'About',
  AudioCompletionRatio: 'Audio completion ratio',
  AudioRatio: 'Audio ratio',
  AutomaticDisableChannelEnabled: 'Auto-disable channels',
  AutomaticDisableKeywords: 'Auto-disable keywords',
  AutomaticDisableStatusCodes: 'Auto-disable status codes',
  AutomaticEnableChannelEnabled: 'Auto-enable channels',
  AutomaticRetryStatusCodes: 'Auto-retry status codes',
  AutoGroups: 'Auto groups',
  CacheRatio: 'Cache ratio',
  ChannelDisableThreshold: 'Channel disable threshold',
  Chats: 'Chat links',
  CheckSensitiveEnabled: 'Sensitive word detection',
  CheckSensitiveOnPromptEnabled: 'Check prompts for sensitive words',
  CompletionRatio: 'Completion ratio',
  CreemApiKey: 'Creem API Key',
  CreemProducts: 'Creem products',
  CreemTestMode: 'Creem test mode',
  CreemWebhookSecret: 'Creem webhook secret',
  CustomCallbackAddress: 'Custom callback address',
  CustomScript: 'Custom script',
  CustomScriptAllowedRules: 'Custom script allowed rules',
  DataExportDefaultTime: 'Data export default time',
  DataExportEnabled: 'Data export',
  DataExportInterval: 'Data export interval',
  DefaultCollapseSidebar: 'Default collapsed sidebar',
  DefaultUseAutoGroup: 'Default auto group',
  DemoSiteEnabled: 'Demo site mode',
  DisplayInCurrencyEnabled: 'Display in currency',
  DisplayTokenStatEnabled: 'Display token statistics',
  DrawingEnabled: 'Drawing',
  EmailAliasRestrictionEnabled: 'Email alias restriction',
  EmailDomainRestrictionEnabled: 'Email domain restriction',
  EmailDomainWhitelist: 'Email domain whitelist',
  EmailLocalPartRules: 'Email local-part rules',
  EmailVerificationEnabled: 'Email verification',
  EpayId: 'Epay merchant ID',
  EpayKey: 'Epay key',
  ExposeRatioEnabled: 'Expose ratio',
  FileDownloadPermission: 'File download permission',
  FileUploadPermission: 'File upload permission',
  Footer: 'Footer',
  GitHubClientId: 'GitHub client ID',
  GitHubClientSecret: 'GitHub client secret',
  GitHubOAuthEnabled: 'GitHub OAuth',
  GroupGroupRatio: 'Group-to-group ratio',
  GroupRatio: 'Group ratio',
  GroupSpecialUsableGroup: 'Group special usable groups',
  HeaderNavModules: 'Header navigation',
  HomePageContent: 'Home page content',
  ImageDownloadPermission: 'Image download permission',
  ImageRatio: 'Image ratio',
  ImageUploadPermission: 'Image upload permission',
  InviteRewardNotifySecret: 'Invitation reward notification secret',
  InviteRewardNotifyUrl: 'Invitation reward notification URL',
  LinuxDOOAuthEnabled: 'LinuxDO OAuth',
  Logo: 'Logo',
  LogConsumeEnabled: 'Usage log recording',
  MinTopUp: 'Minimum top-up amount',
  MjAccountFilterEnabled: 'Midjourney account filter',
  MjActionCheckSuccessEnabled: 'Midjourney action success check',
  MjForwardUrlEnabled: 'Midjourney forward URL',
  MjModeClearEnabled: 'Midjourney mode cleanup',
  MjNotifyEnabled: 'Midjourney callback notification',
  ModelPrice: 'Model price',
  ModelRatio: 'Model ratio',
  ModelRequestRateLimitCount: 'Model request rate limit count',
  ModelRequestRateLimitDurationMinutes:
    'Model request rate limit duration',
  ModelRequestRateLimitEnabled: 'Model request rate limiting',
  ModelRequestRateLimitGroup: 'Model request rate limit groups',
  ModelRequestRateLimitSuccessCount:
    'Model request success rate limit count',
  Notice: 'System Notice',
  PasswordLoginEnabled: 'Password login',
  PasswordRegisterEnabled: 'Password registration',
  PayAddress: 'Payment gateway address',
  PayMethods: 'Payment methods',
  PreConsumedQuota: 'Pre-consumed quota',
  Price: 'Quota price',
  QuotaForInvitee: 'Invitee reward quota',
  QuotaForInviter: 'Inviter reward quota',
  QuotaForNewUser: 'New user quota',
  QuotaPerUnit: 'Quota per unit',
  QuotaRemindThreshold: 'Quota reminder threshold',
  RegisterEnabled: 'Registration',
  RetryTimes: 'Retry times',
  SelfUseModeEnabled: 'Self-use mode',
  SensitiveWords: 'Sensitive words',
  ServerAddress: 'Server Address',
  SidebarModulesAdmin: 'Sidebar modules',
  SMTPAccount: 'SMTP account',
  SMTPForceAuthLogin: 'SMTP force AUTH LOGIN',
  SMTPFrom: 'SMTP sender',
  SMTPInsecureSkipVerify: 'SMTP insecure skip verify',
  SMTPPort: 'SMTP port',
  SMTPServer: 'SMTP server',
  SMTPSSLEnabled: 'SMTP SSL',
  SMTPStartTLSEnabled: 'SMTP STARTTLS',
  SMTPToken: 'SMTP token',
  StopOnSensitiveEnabled: 'Stop on sensitive words',
  StreamCacheQueueLength: 'Stream cache queue length',
  StripeApiSecret: 'Stripe API secret',
  StripeMinTopUp: 'Stripe minimum top-up',
  StripePriceId: 'Stripe price ID',
  StripePromotionCodesEnabled: 'Stripe promotion codes',
  StripeUnitPrice: 'Stripe unit price',
  StripeWebhookSecret: 'Stripe webhook secret',
  SystemName: 'System Name',
  TaskEnabled: 'Task API',
  TelegramBotName: 'Telegram bot name',
  TelegramBotToken: 'Telegram bot token',
  TelegramOAuthEnabled: 'Telegram OAuth',
  theme_frontend: 'Frontend Theme',
  'theme.frontend': 'Frontend Theme',
  TopupGroupRatio: 'Top-up group ratio',
  TopUpLink: 'Top-up link',
  TurnstileCheckEnabled: 'Turnstile check',
  TurnstileSecretKey: 'Turnstile secret key',
  TurnstileSiteKey: 'Turnstile site key',
  USDExchangeRate: 'USD exchange rate',
  UserUsableGroups: 'User usable groups',
  WeChatAccountQRCodeImageURL: 'WeChat account QR code image URL',
  WeChatAuthEnabled: 'WeChat authentication',
  WeChatServerAddress: 'WeChat server address',
  WeChatServerToken: 'WeChat server token',
  WaffoApiKey: 'Waffo API key',
  WaffoCurrency: 'Waffo currency',
  WaffoEnabled: 'Waffo enabled',
  WaffoMerchantId: 'Waffo merchant ID',
  WaffoMinTopUp: 'Waffo minimum top-up',
  WaffoNotifyUrl: 'Waffo notify URL',
  WaffoPancakeMerchantID: 'Waffo Pancake merchant ID',
  WaffoPancakeMinTopUp: 'Waffo Pancake minimum top-up',
  WaffoPancakePrivateKey: 'Waffo Pancake private key',
  WaffoPancakeProductID: 'Waffo Pancake product ID',
  WaffoPancakeReturnURL: 'Waffo Pancake return URL',
  WaffoPancakeStoreID: 'Waffo Pancake store ID',
  WaffoPancakeUnitPrice: 'Waffo Pancake unit price',
  WaffoPayMethods: 'Waffo payment methods',
  WaffoPrivateKey: 'Waffo private key',
  WaffoPublicCert: 'Waffo public certificate',
  WaffoReturnUrl: 'Waffo return URL',
  WaffoSandbox: 'Waffo sandbox',
  WaffoSandboxApiKey: 'Waffo sandbox API key',
  WaffoSandboxPrivateKey: 'Waffo sandbox private key',
  WaffoSandboxPublicCert: 'Waffo sandbox public certificate',
  WaffoSubscriptionReturnUrl: 'Waffo subscription return URL',
  WaffoUnitPrice: 'Waffo unit price',
  WorkerAllowHttpImageRequestEnabled: 'Worker allow HTTP image request',
  WorkerUrl: 'Worker URL',
  WorkerValidKey: 'Worker valid key',
  'claude.default_max_tokens': 'Claude default max tokens',
  'claude.model_headers_settings': 'Claude model headers',
  'claude.thinking_adapter_budget_tokens_percentage':
    'Claude thinking budget percentage',
  'claude.thinking_adapter_enabled': 'Claude thinking adapter',
  'fetch_setting.allowed_ports': 'Allowed ports',
  'fetch_setting.allow_private_ip': 'Allow private IP',
  'fetch_setting.apply_ip_filter_for_domain':
    'Apply IP filter for domains',
  'fetch_setting.domain_filter_mode': 'Domain filter mode',
  'fetch_setting.domain_list': 'Domain list',
  'fetch_setting.enable_ssrf_protection': 'SSRF protection',
  'fetch_setting.ip_filter_mode': 'IP filter mode',
  'fetch_setting.ip_list': 'IP list',
  'gemini.function_call_thought_signature_enabled':
    'Gemini function call thought signature',
  'gemini.remove_function_response_id_enabled':
    'Gemini remove function response ID',
  'gemini.safety_settings': 'Gemini safety settings',
  'gemini.supported_imagine_models': 'Gemini supported imagine models',
  'gemini.thinking_adapter_budget_tokens_percentage':
    'Gemini thinking budget percentage',
  'gemini.thinking_adapter_enabled': 'Gemini thinking adapter',
  'gemini.version_settings': 'Gemini version settings',
  'general_setting.custom_currency_exchange_rate':
    'Custom currency exchange rate',
  'general_setting.custom_currency_symbol': 'Custom currency symbol',
  'general_setting.docs_link': 'Documentation link',
  'general_setting.ping_interval_enabled': 'Ping interval',
  'general_setting.ping_interval_seconds': 'Ping interval seconds',
  'general_setting.quota_display_type': 'Quota display type',
  'global.chat_completions_to_responses_policy':
    'Chat completions to responses policy',
  'global.pass_through_request_enabled': 'Pass-through requests',
  'global.thinking_model_blacklist': 'Thinking model blacklist',
  'grok.violation_deduction_amount': 'Grok violation deduction amount',
  'grok.violation_deduction_enabled': 'Grok violation deduction',
  'legal.privacy_policy': 'Privacy policy',
  'legal.user_agreement': 'User agreement',
  'model_deployment.ionet.api_key': 'io.net API key',
  'model_deployment.ionet.enabled': 'io.net deployment',
  'monitor_setting.auto_test_channel_enabled': 'Automatic channel testing',
  'monitor_setting.auto_test_channel_minutes':
    'Automatic channel test interval',
  'monitor_setting.channel_test_mode': 'Channel test mode',
  'payment_setting.amount_discount': 'Amount discount',
  'payment_setting.amount_options': 'Amount options',
  'perf_metrics_setting.bucket_time': 'Metrics bucket time',
  'perf_metrics_setting.enabled': 'Performance metrics',
  'perf_metrics_setting.flush_interval': 'Metrics flush interval',
  'perf_metrics_setting.retention_days': 'Metrics retention days',
  'performance_setting.disk_cache_enabled': 'Disk cache',
  'performance_setting.disk_cache_max_size_mb': 'Disk cache max size',
  'performance_setting.disk_cache_path': 'Disk cache path',
  'performance_setting.disk_cache_threshold_mb': 'Disk cache threshold',
  'performance_setting.monitor_cpu_threshold': 'CPU monitor threshold',
  'performance_setting.monitor_disk_threshold': 'Disk monitor threshold',
  'performance_setting.monitor_enabled': 'Performance monitor',
  'performance_setting.monitor_memory_threshold': 'Memory monitor threshold',
  'quota_setting.enable_free_model_pre_consume':
    'Free model pre-consumption',
  'token_setting.max_user_tokens': 'Maximum tokens per user',
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

export function getOperationTargetTypeLabelKey(targetType: string): string {
  return OPERATION_TARGET_TYPE_LABELS[targetType] ?? targetType
}

export function getOperationOptionLabelKey(optionKey: string): string {
  return OPERATION_OPTION_LABELS[optionKey] ?? optionKey
}

export function getOperationTargetIdLabelKey(
  targetType: string,
  targetId: string
): string {
  if (targetType === 'option') {
    return getOperationOptionLabelKey(targetId)
  }
  return targetId
}

export function hasOperationOptionLabel(optionKey: string): boolean {
  return Object.prototype.hasOwnProperty.call(OPERATION_OPTION_LABELS, optionKey)
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
