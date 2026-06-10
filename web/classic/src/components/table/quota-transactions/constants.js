/*
Copyright (C) 2025-2026 QuantumNous

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

export const QUOTA_TRANSACTION_TYPES = [
  { value: 'topup', label: '充值' },
  { value: 'gift', label: '赠送' },
  { value: 'redemption', label: '兑换码' },
  { value: 'refund', label: '退款' },
  { value: 'admin_adjust', label: '管理员调整' },
  { value: 'subscription_buy', label: '订阅购买' },
  { value: 'subscription_grant', label: '订阅发放' },
  { value: 'checkin', label: '签到' },
];

export const QUOTA_TRANSACTION_TYPE_COLORS = {
  topup: 'green',
  gift: 'green',
  redemption: 'green',
  refund: 'green',
  admin_adjust: 'blue',
  subscription_buy: 'red',
  subscription_grant: 'green',
  checkin: 'green',
};

export function getQuotaTransactionTypeLabel(type) {
  const meta = QUOTA_TRANSACTION_TYPES.find((t) => t.value === type);
  return meta ? meta.label : type;
}

export const QUOTA_TRANSACTION_SOURCES = {
  redemption: '兑换码',
  relay: '接口调用',
  admin: '管理员',
  legacy: '历史数据',
  system: '系统',
  invite: '邀请奖励',
  register: '注册赠送',
  checkin: '签到',
  subscription: '订阅',
  stripe: 'Stripe 充值',
  epay: '易支付充值',
  creem: 'Creem 充值',
  waffo: 'Waffo 充值',
  waffo_pancake: 'Waffo Pancake 充值',
};

export const QUOTA_TRANSACTION_REFERENCE_TYPES = {
  redemption: '兑换码',
  request: '请求',
  user: '用户',
  manual: '手动',
  subscription: '订阅',
  legacy_user_gift_quota: '历史赠送额度',
};

export function getQuotaTransactionSourceLabel(source) {
  return QUOTA_TRANSACTION_SOURCES[source] || source;
}

export function getQuotaTransactionReferenceTypeLabel(refType) {
  return QUOTA_TRANSACTION_REFERENCE_TYPES[refType] || refType;
}
