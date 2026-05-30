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

import React, { useRef } from 'react';
import { Button, Form } from '@douyinfe/semi-ui';
import { IconSearch } from '@douyinfe/semi-icons';

import { DATE_RANGE_PRESETS } from '../../../constants/console.constants';
import { OPERATION_LOG_CATEGORIES } from './constants';

const OperationLogsFilters = ({
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

          {/* 操作类别 */}
          <Form.Select
            field='category'
            placeholder={t('操作类别')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            {OPERATION_LOG_CATEGORIES.map((c) => (
              <Form.Select.Option key={c.value} value={c.value}>
                {t(c.label)}
              </Form.Select.Option>
            ))}
          </Form.Select>

          {/* 结果 */}
          <Form.Select
            field='success'
            placeholder={t('结果')}
            className='w-full'
            showClear
            pure
            size='small'
          >
            <Form.Select.Option value='1'>{t('成功')}</Form.Select.Option>
            <Form.Select.Option value='0'>{t('失败')}</Form.Select.Option>
          </Form.Select>

          {isAdminUser && (
            <>
              <Form.Input
                field='operator_name'
                prefix={<IconSearch />}
                placeholder={t('操作者')}
                showClear
                pure
                size='small'
              />
              <Form.Input
                field='target_type'
                prefix={<IconSearch />}
                placeholder={t('目标类型')}
                showClear
                pure
                size='small'
              />
              <Form.Input
                field='target_id'
                prefix={<IconSearch />}
                placeholder={t('目标 ID')}
                showClear
                pure
                size='small'
              />
              <Form.Input
                field='ip'
                prefix={<IconSearch />}
                placeholder={t('IP')}
                showClear
                pure
                size='small'
              />
            </>
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

export default OperationLogsFilters;
