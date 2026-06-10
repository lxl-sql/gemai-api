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

import React from 'react';
import { Tag, Typography, Popover } from '@douyinfe/semi-ui';
import {
  renderQuota,
  timestamp2string,
} from '../../../helpers';
import {
  QUOTA_TRANSACTION_TYPE_COLORS,
  getQuotaTransactionTypeLabel,
  getQuotaTransactionSourceLabel,
  getQuotaTransactionReferenceTypeLabel,
} from './constants';

const { Text, Paragraph } = Typography;

const formatDelta = (value) => {
  const num = Number(value || 0);
  if (num === 0) return '-';
  return `${num > 0 ? '+' : ''}${renderQuota(num)}`;
};

const deltaColor = (value) => {
  const num = Number(value || 0);
  if (num > 0) return 'green';
  if (num < 0) return 'red';
  return 'grey';
};

const getRoundedUSDValue = (quota) => {
  let quotaPerUnit = localStorage.getItem('quota_per_unit');
  const quotaDisplayType = localStorage.getItem('quota_display_type') || 'USD';
  quotaPerUnit = parseFloat(quotaPerUnit) || 500000;
  if (quotaDisplayType === 'TOKENS') {
    return quota;
  }
  const resultUSD = quota / quotaPerUnit;
  let rate = 1;
  if (quotaDisplayType === 'CNY') {
    const statusStr = localStorage.getItem('status');
    try {
      if (statusStr) {
        const s = JSON.parse(statusStr);
        rate = s?.usd_exchange_rate || 1;
      }
    } catch (e) {}
  } else if (quotaDisplayType === 'CUSTOM') {
    const statusStr = localStorage.getItem('status');
    try {
      if (statusStr) {
        const s = JSON.parse(statusStr);
        rate = s?.custom_currency_exchange_rate || 1;
      }
    } catch (e) {}
  }
  const value = resultUSD * rate;
  return Number(value.toFixed(2));
};

const formatCurrencyValue = (value) => {
  const quotaDisplayType = localStorage.getItem('quota_display_type') || 'USD';
  let symbol = '$';
  if (quotaDisplayType === 'CNY') {
    symbol = '¥';
  } else if (quotaDisplayType === 'CUSTOM') {
    const statusStr = localStorage.getItem('status');
    try {
      if (statusStr) {
        const s = JSON.parse(statusStr);
        symbol = s?.custom_currency_symbol || '¤';
      }
    } catch (e) {}
  } else if (quotaDisplayType === 'TOKENS') {
    return value.toLocaleString();
  }
  return symbol + value.toFixed(2);
};

export const getQuotaTransactionsColumns = ({ t, isAdminUser }) => {
  const columns = [
    {
      title: t('ID'),
      dataIndex: 'id',
    },
    {
      title: t('时间'),
      dataIndex: 'created_at',
      render: (text) => (
        <span className='font-mono'>{timestamp2string(text)}</span>
      ),
    },
  ];

  if (isAdminUser) {
    columns.push({
      title: t('用户'),
      dataIndex: 'username',
      render: (text, record) => (
        <div>
          <div>{text || '-'}</div>
          <Text type='secondary' size='small'>
            ID {record.user_id}
          </Text>
        </div>
      ),
    });
  }

  columns.push(
    {
      title: t('类型'),
      dataIndex: 'type',
      render: (text) => (
        <Tag color={QUOTA_TRANSACTION_TYPE_COLORS[text] || 'grey'} shape='circle'>
          {t(getQuotaTransactionTypeLabel(text))}
        </Tag>
      ),
    },
    {
      title: t('额度变化'),
      render: (_, record) => {
        const quotaDelta = record.quota_delta || 0;
        const giftQuotaDelta = record.gift_quota_delta || 0;

        if (quotaDelta !== 0 && giftQuotaDelta !== 0) {
          const roundedQuota = getRoundedUSDValue(quotaDelta);
          const roundedGift = getRoundedUSDValue(giftQuotaDelta);
          const roundedTotal = roundedQuota + roundedGift;
          const displayTotal = `${roundedTotal > 0 ? '+' : ''}${formatCurrencyValue(roundedTotal)}`;
          return (
            <div className='flex flex-col gap-1 text-xs py-1 font-mono'>
              <div className='flex items-center gap-1.5'>
                <span className='text-gray-500 text-[11px]'>{t('充值')}:</span>
                <Tag color={deltaColor(quotaDelta)} shape='circle' size='small'>
                  {formatDelta(quotaDelta)}
                </Tag>
              </div>
              <div className='flex items-center gap-1.5'>
                <span className='text-gray-500 text-[11px]'>{t('赠送')}:</span>
                <Tag color={deltaColor(giftQuotaDelta)} shape='circle' size='small'>
                  {formatDelta(giftQuotaDelta)}
                </Tag>
              </div>
              <div className='flex items-center gap-1.5 font-semibold mt-0.5 border-t border-dashed pt-0.5 border-gray-200 dark:border-gray-800'>
                <span className='text-[11px]'>{t('合计')}:</span>
                <span style={{ color: roundedTotal > 0 ? 'var(--semi-color-success)' : 'var(--semi-color-danger)' }}>
                  {displayTotal}
                </span>
              </div>
            </div>
          );
        }

        if (quotaDelta !== 0) {
          return (
            <div className='flex items-center gap-1.5 font-mono'>
              <span className='text-gray-500 text-[11px]'>{t('充值')}:</span>
              <Tag color={deltaColor(quotaDelta)} shape='circle'>
                {formatDelta(quotaDelta)}
              </Tag>
            </div>
          );
        }

        if (giftQuotaDelta !== 0) {
          return (
            <div className='flex items-center gap-1.5 font-mono'>
              <span className='text-gray-500 text-[11px]'>{t('赠送')}:</span>
              <Tag color={deltaColor(giftQuotaDelta)} shape='circle'>
                {formatDelta(giftQuotaDelta)}
              </Tag>
            </div>
          );
        }

        return <span className='text-gray-400 font-mono'>-</span>;
      },
    },
    {
      title: t('变动后余额'),
      dataIndex: 'balance_after',
      render: (_, record) => {
        const remain = (record.balance_after || 0) + (record.gift_balance_after || 0);
        const popoverContent = (
          <div className='text-xs p-2'>
            <Paragraph copyable={{ content: renderQuota(record.balance_after || 0) }}>
              {t('充值额度')}: {renderQuota(record.balance_after || 0)}
            </Paragraph>
            <Paragraph copyable={{ content: renderQuota(record.gift_balance_after || 0) }}>
              {t('赠送额度')}: {renderQuota(record.gift_balance_after || 0)}
            </Paragraph>
            <Paragraph copyable={{ content: renderQuota(remain) }}>
              {t('变动后余额')}: {renderQuota(remain)}
            </Paragraph>
          </div>
        );

        return (
          <Popover content={popoverContent} position='top'>
            <Tag
              color='white'
              shape='circle'
              style={{ height: 'auto', padding: '6px 12px' }}
            >
              <div className='flex flex-col items-center justify-center w-full gap-1'>
                <span
                  className='text-xs font-bold leading-none'
                  style={{ color: 'var(--semi-color-text-0)' }}
                >
                  {renderQuota(remain)}
                </span>
                <span
                  className='text-[10px] leading-none text-gray-500'
                  style={{ whiteSpace: 'nowrap' }}
                >
                  {t('充值')}: {renderQuota(record.balance_after || 0)} · {t('赠送')}:{' '}
                  {renderQuota(record.gift_balance_after || 0)}
                </span>
              </div>
            </Tag>
          </Popover>
        );
      },
    },
    {
      title: t('来源'),
      dataIndex: 'source',
      render: (text, record) => {
        const refTypeLabel = t(getQuotaTransactionReferenceTypeLabel(record.reference_type));
        const reference = record.reference_id ? `${refTypeLabel} #${record.reference_id}` : refTypeLabel;
        return (
          <div>
            <div>{t(getQuotaTransactionSourceLabel(text)) || '-'}</div>
            <Text type='secondary' size='small'>
              {reference || '-'}
            </Text>
          </div>
        );
      },
    },
  );

  return columns;
};
