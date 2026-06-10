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

import React, { useRef } from 'react';
import { Button, Form } from '@douyinfe/semi-ui';
import { IconSearch } from '@douyinfe/semi-icons';

import { DATE_RANGE_PRESETS } from '../../../constants/console.constants';
import { QUOTA_TRANSACTION_TYPES } from './constants';

const QuotaTransactionsFilters = ({
  formInitValues,
  setFormApi,
  refresh,
  loading,
  isAdminUser,
  t,
}) => {
  const formApiRef = useRef(null);

  const handleReset = () => {
    if (!formApiRef.current) return;
    formApiRef.current.reset();
    setTimeout(() => {
      refresh();
    }, 100);
  };

  return (
    <Form
      initValues={formInitValues}
      getFormApi={(api) => {
        setFormApi(api);
        formApiRef.current = api;
      }}
      onSubmit={refresh}
      allowEmpty={true}
      autoComplete='off'
      layout='vertical'
      trigger='change'
      stopValidateWithError={false}
    >
      <div className='flex flex-col gap-2'>
        <div className='grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-2'>
          {/* 时间选择器 */}
          <div className='col-span-1 lg:col-span-2'>
            <Form.DatePicker
              field='dateRange'
              className='w-full'
              type='dateTimeRange'
              placeholder={[t('开始时间'), t('结束时间')]}
              showClear
              pure
              size='small'
              presets={DATE_RANGE_PRESETS.map((preset) => ({
                text: t(preset.text),
                start: preset.start(),
                end: preset.end(),
              }))}
            />
          </div>

          {/* 类型 */}
          <Form.Select
            field='type'
            placeholder={t('类型')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            {QUOTA_TRANSACTION_TYPES.map((type) => (
              <Form.Select.Option key={type.value} value={type.value}>
                {t(type.label)}
              </Form.Select.Option>
            ))}
          </Form.Select>

          {/* 方向 */}
          <Form.Select
            field='direction'
            placeholder={t('方向')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            <Form.Select.Option value='income'>{t('收入')}</Form.Select.Option>
            <Form.Select.Option value='expense'>{t('支出')}</Form.Select.Option>
          </Form.Select>

          {/* 额度类型 */}
          <Form.Select
            field='bucket'
            placeholder={t('额度类型')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            <Form.Select.Option value='recharge'>{t('充值额度')}</Form.Select.Option>
            <Form.Select.Option value='gift'>{t('赠送额度')}</Form.Select.Option>
            <Form.Select.Option value='both'>{t('两类额度')}</Form.Select.Option>
          </Form.Select>

          {isAdminUser && (
            <Form.Input
              field='username'
              prefix={<IconSearch />}
              placeholder={t('用户名')}
              showClear
              pure
              size='small'
            />
          )}
        </div>

        {/* 操作按钮区域 */}
        <div className='flex justify-end'>
          <div className='flex gap-2 w-full sm:w-auto justify-end'>
            <Button
              type='tertiary'
              htmlType='submit'
              loading={loading}
              size='small'
            >
              {t('查询')}
            </Button>
            <Button
              type='tertiary'
              onClick={handleReset}
              size='small'
            >
              {t('重置')}
            </Button>
          </div>
        </div>
      </div>
    </Form>
  );
};

export default QuotaTransactionsFilters;
