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

import React, { useMemo, useState, useCallback } from 'react';
import { Empty, SideSheet, Tag, Descriptions, Button } from '@douyinfe/semi-ui';
import { IconCopy } from '@douyinfe/semi-icons';
import CardTable from '../../common/ui/CardTable';
import {
  OPERATION_CATEGORY_COLORS,
  getOperationActionLabel,
  getOperationCategoryLabel,
  getOperatorRoleLabel,
} from './constants';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { getOperationLogsColumns } from './OperationLogsColumnDefs';

const OperationLogsTable = (logsData) => {
  const {
    logs,
    expandData,
    hasExpandableRows,
    compactMode,
    loading,
    activePage,
    pageSize,
    logCount,
    handlePageChange,
    handlePageSizeChange,
    copyText,
    isAdminUser,
    t,
  } = logsData;

  const [sheetVisible, setSheetVisible] = useState(false);
  const [activeLog, setActiveLog] = useState(null);

  const handleViewDetail = useCallback((record) => {
    setActiveLog(record);
    setSheetVisible(true);
  }, []);

  const columns = useMemo(() => {
    return getOperationLogsColumns({
      t,
      isAdminUser,
      copyText,
      onViewDetail: handleViewDetail,
    });
  }, [t, isAdminUser, copyText, handleViewDetail]);

  const tableColumns = useMemo(() => {
    return compactMode
      ? columns.map((col) => {
          if (col.dataIndex === 'operate') {
            const { fixed, ...rest } = col;
            return rest;
          }
          return col;
        })
      : columns;
  }, [compactMode, columns]);

  const formatJSON = (str) => {
    try {
      const parsed = JSON.parse(str);
      return JSON.stringify(parsed, null, 2);
    } catch (e) {
      return str;
    }
  };

  // 统一的卡片样式令牌，保证侧边栏内各区块圆角/描边/留白一致。
  const panelStyle = {
    backgroundColor: 'var(--semi-color-fill-0)',
    borderRadius: '12px',
    border: '1px solid var(--semi-color-border)',
  };

  const sectionLabelStyle = {
    fontSize: '13px',
    fontWeight: 600,
    color: 'var(--semi-color-text-0)',
  };

  // 展开行：仅展示 User Agent，使用带内边距的容器避免与表格行贴边。
  const expandRowRender = (record) => {
    const ua = record.user_agent;
    if (!ua) return null;
    return (
      <div className='px-4 py-3'>
        <div className='flex items-baseline gap-2'>
          <span
            className='shrink-0'
            style={{ fontSize: '12px', fontWeight: 600, color: 'var(--semi-color-text-2)' }}
          >
            {t('User Agent')}
          </span>
          <span
            className='font-mono'
            style={{
              fontSize: '12px',
              color: 'var(--semi-color-text-1)',
              wordBreak: 'break-word',
              lineHeight: 1.6,
            }}
          >
            {ua}
          </span>
        </div>
      </div>
    );
  };

  return (
    <>
      <CardTable
        columns={tableColumns}
        {...(hasExpandableRows() && {
          expandedRowRender: expandRowRender,
          expandRowByClick: true,
          rowExpandable: (record) =>
            expandData[record.key] && expandData[record.key].length > 0,
        })}
        dataSource={logs}
        scroll={{ x: 'max-content' }}
        rowKey='key'
        loading={loading}
        className='rounded-xl overflow-hidden'
        size='small'
        empty={
          <Empty
            image={<IllustrationNoResult style={{ width: 150, height: 150 }} />}
            darkModeImage={
              <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
            }
            description={t('搜索无结果')}
            style={{ padding: 30 }}
          />
        }
        pagination={{
          currentPage: activePage,
          pageSize: pageSize,
          total: logCount,
          pageSizeOptions: [10, 20, 50, 100],
          showSizeChanger: true,
          onPageSizeChange: (size) => {
            handlePageSizeChange(size);
          },
          onPageChange: handlePageChange,
        }}
        hidePagination={true}
      />

      <SideSheet
        title={t('操作日志详情')}
        visible={sheetVisible}
        onCancel={() => setSheetVisible(false)}
        width={520}
        bodyStyle={{ padding: 0 }}
      >
        {activeLog && (
          <div className='px-5 py-5 space-y-4 text-sm'>
            <div style={{ ...panelStyle, padding: '4px 16px' }}>
              <Descriptions
                align='plain'
                size='small'
                column={1}
                data={[
                  { key: t('时间'), value: <span className='font-mono'>{activeLog.timestamp2string}</span> },
                  ...(isAdminUser ? [{ key: t('操作者'), value: activeLog.operator_name ? `${activeLog.operator_name} (${t(getOperatorRoleLabel(activeLog.operator_role))} · ID: ${activeLog.operator_id})` : '-' }] : []),
                  { key: t('操作类别'), value: <Tag color={OPERATION_CATEGORY_COLORS[activeLog.category] || 'grey'} size='small' shape='circle'>{t(getOperationCategoryLabel(activeLog.category))}</Tag> },
                  { key: t('操作类型'), value: <span style={{ fontWeight: 500 }}>{t(getOperationActionLabel(activeLog.action))}</span> },
                  { key: t('结果'), value: <Tag color={activeLog.success ? 'green' : 'red'} size='small' shape='circle'>{activeLog.success ? t('成功') : t('失败')}</Tag> },
                  ...(isAdminUser && activeLog.ip ? [{ key: t('IP'), value: <span className='font-mono'>{activeLog.ip}</span> }] : [])
                ]}
              />
            </div>

            {activeLog.user_agent && (
              <div className='space-y-2'>
                <div className='flex items-center justify-between'>
                  <span style={sectionLabelStyle}>{t('User Agent')}</span>
                  <Button
                    icon={<IconCopy />}
                    size='small'
                    theme='borderless'
                    onClick={(e) => {
                      copyText(e, activeLog.user_agent);
                    }}
                  >
                    {t('复制 UA')}
                  </Button>
                </div>
                <div
                  style={{
                    ...panelStyle,
                    padding: '12px 14px',
                    fontSize: '12px',
                    fontFamily: 'monospace',
                    wordBreak: 'break-word',
                    lineHeight: 1.6,
                    color: 'var(--semi-color-text-1)',
                  }}
                >
                  {activeLog.user_agent}
                </div>
              </div>
            )}

            {activeLog.detail && (
              <div className='space-y-2'>
                <div className='flex items-center justify-between'>
                  <span style={sectionLabelStyle}>{t('操作参数与详情 (JSON)')}</span>
                  <Button
                    icon={<IconCopy />}
                    size='small'
                    theme='borderless'
                    onClick={(e) => {
                      copyText(e, formatJSON(activeLog.detail));
                    }}
                  >
                    {t('复制 JSON')}
                  </Button>
                </div>
                <pre
                  style={{
                    ...panelStyle,
                    padding: '14px',
                    fontSize: '12px',
                    fontFamily: 'monospace',
                    overflow: 'auto',
                    maxHeight: '380px',
                    lineHeight: 1.6,
                    margin: 0,
                    color: 'var(--semi-color-text-0)',
                  }}
                >
                  {formatJSON(activeLog.detail)}
                </pre>
              </div>
            )}
          </div>
        )}
      </SideSheet>
    </>
  );
};

export default OperationLogsTable;
