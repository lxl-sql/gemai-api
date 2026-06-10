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

import React from 'react';
import { Tag, Button, Space, Popover, Dropdown } from '@douyinfe/semi-ui';
import { IconMore } from '@douyinfe/semi-icons';
import { renderQuota, timestamp2string } from '../../../helpers';
import {
  REDEMPTION_STATUS,
  REDEMPTION_STATUS_MAP,
  REDEMPTION_ACTIONS,
} from '../../../constants/redemption.constants';

/**
 * Check if redemption code is expired
 */
export const isExpired = (record) => {
  return (
    record.status === REDEMPTION_STATUS.UNUSED &&
    record.expired_time !== 0 &&
    record.expired_time < Math.floor(Date.now() / 1000)
  );
};

/**
 * Render timestamp
 */
const renderTimestamp = (timestamp) => {
  return <>{timestamp2string(timestamp)}</>;
};

/**
 * Render redemption code status
 */
const renderStatus = (status, record, t) => {
  if (isExpired(record)) {
    return (
      <Tag color='orange' shape='circle'>
        {t('已过期')}
      </Tag>
    );
  }

  const statusConfig = REDEMPTION_STATUS_MAP[status];
  if (statusConfig) {
    return (
      <Tag color={statusConfig.color} shape='circle'>
        {t(statusConfig.text)}
      </Tag>
    );
  }

  return (
    <Tag color='black' shape='circle'>
      {t('未知状态')}
    </Tag>
  );
};

/**
 * Get redemption code table column definitions
 */
export const getRedemptionsColumns = ({
  t,
  manageRedemption,
  copyText,
  setEditingRedemption,
  setShowEdit,
  refresh,
  redemptions,
  activePage,
  showDeleteRedemptionModal,
}) => {
  return [
    {
      title: t('ID'),
      dataIndex: 'id',
    },
    {
      title: t('名称'),
      dataIndex: 'name',
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      key: 'status',
      render: (text, record) => {
        return <div>{renderStatus(text, record, t)}</div>;
      },
    },
    {
      title: t('额度'),
      width: 150,
      render: (_, record) => {
        const quota = parseInt(record.quota || 0);
        const giftQuota = parseInt(record.gift_quota || 0);
        const totalQuota = quota + giftQuota;

        if (quota > 0 && giftQuota > 0) {
          return (
            <div className='flex flex-col gap-1 text-xs py-1 font-mono'>
              <div className='flex items-center gap-1.5'>
                <span className='text-gray-500 text-[11px]'>{t('充值')}:</span>
                <Tag color='grey' shape='circle' size='small'>
                  {renderQuota(quota)}
                </Tag>
              </div>
              <div className='flex items-center gap-1.5'>
                <span className='text-gray-500 text-[11px]'>{t('赠送')}:</span>
                <Tag color='green' shape='circle' size='small'>
                  {renderQuota(giftQuota)}
                </Tag>
              </div>
              <div className='flex items-center gap-1.5 font-semibold mt-0.5 border-t border-dashed pt-0.5 border-gray-200 dark:border-gray-800'>
                <span className='text-[11px]'>{t('合计')}:</span>
                <span style={{ color: 'var(--semi-color-primary)', fontSize: '12px' }}>
                  {renderQuota(totalQuota)}
                </span>
              </div>
            </div>
          );
        }

        if (quota > 0) {
          return (
            <div className='flex items-center gap-1.5 font-mono text-xs'>
              <span className='text-gray-500 text-[11px]'>{t('充值')}:</span>
              <Tag color='grey' shape='circle'>
                {renderQuota(quota)}
              </Tag>
            </div>
          );
        }

        if (giftQuota > 0) {
          return (
            <div className='flex items-center gap-1.5 font-mono text-xs'>
              <span className='text-gray-500 text-[11px]'>{t('赠送')}:</span>
              <Tag color='green' shape='circle'>
                {renderQuota(giftQuota)}
              </Tag>
            </div>
          );
        }

        return <span className='text-gray-400 font-mono'>-</span>;
      },
    },
    {
      title: t('创建时间'),
      dataIndex: 'created_time',
      render: (text) => {
        return <div>{renderTimestamp(text)}</div>;
      },
    },
    {
      title: t('过期时间'),
      dataIndex: 'expired_time',
      render: (text) => {
        return <div>{text === 0 ? t('永不过期') : renderTimestamp(text)}</div>;
      },
    },
    {
      title: t('兑换人ID'),
      dataIndex: 'used_user_id',
      render: (text) => {
        return <div>{text === 0 ? t('无') : text}</div>;
      },
    },
    {
      title: '',
      dataIndex: 'operate',
      fixed: 'right',
      width: 205,
      render: (text, record) => {
        // Create dropdown menu items for more operations
        const moreMenuItems = [
          {
            node: 'item',
            name: t('删除'),
            type: 'danger',
            onClick: () => {
              showDeleteRedemptionModal(record);
            },
          },
        ];

        if (record.status === REDEMPTION_STATUS.UNUSED && !isExpired(record)) {
          moreMenuItems.push({
            node: 'item',
            name: t('禁用'),
            type: 'warning',
            onClick: () => {
              manageRedemption(record.id, REDEMPTION_ACTIONS.DISABLE, record);
            },
          });
        } else if (!isExpired(record)) {
          moreMenuItems.push({
            node: 'item',
            name: t('启用'),
            type: 'secondary',
            onClick: () => {
              manageRedemption(record.id, REDEMPTION_ACTIONS.ENABLE, record);
            },
            disabled: record.status === REDEMPTION_STATUS.USED,
          });
        }

        return (
          <Space>
            <Popover
              content={record.key}
              style={{ padding: 20 }}
              position='top'
            >
              <Button type='tertiary' size='small'>
                {t('查看')}
              </Button>
            </Popover>
            <Button
              size='small'
              onClick={async () => {
                await copyText(record.key);
              }}
            >
              {t('复制')}
            </Button>
            <Button
              type='tertiary'
              size='small'
              onClick={() => {
                setEditingRedemption(record);
                setShowEdit(true);
              }}
              disabled={record.status !== REDEMPTION_STATUS.UNUSED}
            >
              {t('编辑')}
            </Button>
            <Dropdown
              trigger='click'
              position='bottomRight'
              menu={moreMenuItems}
            >
              <Button type='tertiary' size='small' icon={<IconMore />} />
            </Dropdown>
          </Space>
        );
      },
    },
  ];
};
