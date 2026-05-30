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
import { Button, Tag, Tooltip } from '@douyinfe/semi-ui';
import { IconHelpCircle } from '@douyinfe/semi-icons';
import {
  OPERATION_CATEGORY_COLORS,
  getOperationActionLabel,
  getOperationCategoryLabel,
  getOperatorRoleLabel,
} from './constants';

// 表头标题统一不换行，避免窄视口下中文列名被压成竖排。
const nowrapTitle = (text) => (
  <span className='whitespace-nowrap'>{text}</span>
);

export const getOperationLogsColumns = ({ t, isAdminUser, copyText, onViewDetail }) => {
  const columns = [
    {
      title: nowrapTitle(t('时间')),
      dataIndex: 'created_at',
      key: 'time',
      width: 170,
      render: (text, record) => (
        <span className='font-mono text-xs whitespace-nowrap inline-flex items-center h-full'>
          {record.timestamp2string}
        </span>
      ),
    },
  ];

  // 操作者列仅对管理员有意义（普通用户只能查看自己的记录）。
  if (isAdminUser) {
    columns.push({
      title: nowrapTitle(t('操作者')),
      dataIndex: 'operator_name',
      key: 'operator',
      width: 170,
      render: (text, record) => (
        <div className='flex flex-col'>
          <span className='font-medium whitespace-nowrap'>{record.operator_name || '-'}</span>
          <span className='text-xs text-gray-400 whitespace-nowrap'>
            {t(getOperatorRoleLabel(record.operator_role))}
            {record.operator_id > 0 ? ` · ID ${record.operator_id}` : ''}
          </span>
        </div>
      ),
    });
  }

  columns.push(
    {
      title: nowrapTitle(t('操作类别')),
      dataIndex: 'category',
      key: 'category',
      width: 120,
      render: (text) => {
        if (!text) return '-';
        return (
          <Tag
            color={OPERATION_CATEGORY_COLORS[text] || 'grey'}
            shape='circle'
            size='small'
          >
            {t(getOperationCategoryLabel(text))}
          </Tag>
        );
      },
    },
    {
      title: nowrapTitle(t('操作类型')),
      dataIndex: 'action',
      key: 'action',
      width: 120,
      render: (text) => (
        <span className='text-sm font-medium whitespace-nowrap'>
          {t(getOperationActionLabel(text))}
        </span>
      ),
    },
    {
      title: nowrapTitle(t('目标')),
      dataIndex: 'target_type',
      key: 'target',
      width: 120,
      render: (text, record) => {
        if (!record.target_type && !record.target_id) return '-';
        const value = `${record.target_type || ''}${record.target_id ? ` #${record.target_id}` : ''}`;
        return <span className='font-mono text-xs whitespace-nowrap'>{value}</span>;
      },
    },
    {
      title: nowrapTitle(t('结果')),
      dataIndex: 'success',
      key: 'success',
      width: 90,
      render: (success) => (
        <Tag color={success ? 'green' : 'red'} shape='circle' size='small'>
          {success ? t('成功') : t('失败')}
        </Tag>
      ),
    },
  );

  // IP 仅对管理员展示。
  if (isAdminUser) {
    columns.push({
      title: (
        <div className='flex items-center gap-1 whitespace-nowrap'>
          {t('IP')}
          <Tooltip
            content={t('进行此次操作的客户端 IP 地址')}
          >
            <IconHelpCircle className='text-gray-400 cursor-help' />
          </Tooltip>
        </div>
      ),
      dataIndex: 'ip',
      key: 'ip',
      width: 120,
      render: (text) =>
        text ? (
          <Tooltip content={text}>
            <span>
              <Tag
                color='orange'
                shape='circle'
                size='small'
                onClick={(e) => copyText(e, text)}
                style={{ cursor: 'pointer' }}
              >
                {text}
              </Tag>
            </span>
          </Tooltip>
        ) : (
          '-'
        ),
    });

    columns.push({
      title: '',
      dataIndex: 'operate',
      key: 'detail',
      fixed: 'right',
      width: 112,
      render: (text, record) => {
        if (!record.detail && !record.user_agent) return '-';
        return (
          <Button
            type='tertiary'
            size='small'
            onClick={(e) => {
              e.stopPropagation();
              onViewDetail(record);
            }}
          >
            {t('查看详情')}
          </Button>
        );
      },
    });
  }

  return columns;
};
