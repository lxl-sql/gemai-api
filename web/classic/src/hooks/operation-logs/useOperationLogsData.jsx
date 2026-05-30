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

import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal } from '@douyinfe/semi-ui';
import {
  API,
  copy,
  getTodayStartTimestamp,
  isAdmin,
  showError,
  showSuccess,
  timestamp2string,
} from '../../helpers';
import { ITEMS_PER_PAGE } from '../../constants';
import { useTableCompactMode } from '../common/useTableCompactMode';

export const useOperationLogsData = () => {
  const { t } = useTranslation();

  const [logs, setLogs] = useState([]);
  const [expandData, setExpandData] = useState({});
  const [compactMode, setCompactMode] = useTableCompactMode('operation-logs');
  const [loading, setLoading] = useState(false);
  const [activePage, setActivePage] = useState(1);
  const [logCount, setLogCount] = useState(0);
  const [pageSize, setPageSize] = useState(ITEMS_PER_PAGE);
  const [formApi, setFormApi] = useState(null);

  const isAdminUser = isAdmin();
  const now = new Date();

  const formInitValues = {
    dateRange: [
      timestamp2string(getTodayStartTimestamp()),
      timestamp2string(now.getTime() / 1000 + 3600),
    ],
    category: '',
    success: '',
    operator_name: '',
    target_type: '',
    target_id: '',
    ip: '',
  };

  const getFormValues = () => {
    const formValues = formApi ? formApi.getValues() : {};

    let start_timestamp = timestamp2string(getTodayStartTimestamp());
    let end_timestamp = timestamp2string(now.getTime() / 1000 + 3600);

    if (
      formValues.dateRange &&
      Array.isArray(formValues.dateRange) &&
      formValues.dateRange.length === 2
    ) {
      start_timestamp = formValues.dateRange[0];
      end_timestamp = formValues.dateRange[1];
    }

    return {
      category: formValues.category || '',
      success: formValues.success || '',
      operator_name: formValues.operator_name || '',
      target_type: formValues.target_type || '',
      target_id: formValues.target_id || '',
      ip: formValues.ip || '',
      start_timestamp,
      end_timestamp,
    };
  };

  const buildExpandData = (item) => {
    const rows = [];
    if (item.user_agent) {
      rows.push({ key: t('User Agent'), value: item.user_agent });
    }
    return rows;
  };

  const setLogsFormat = (items) => {
    const nextExpandData = {};
    const formatted = (items || []).map((item) => {
      const row = {
        ...item,
        key: item.id,
        timestamp2string: timestamp2string(item.created_at),
      };
      nextExpandData[row.key] = buildExpandData(item);
      return row;
    });
    setLogs(formatted);
    setExpandData(nextExpandData);
  };

  const loadLogs = async (startIdx, size) => {
    setLoading(true);
    try {
      const {
        category,
        success,
        operator_name,
        target_type,
        target_id,
        ip,
        start_timestamp,
        end_timestamp,
      } = getFormValues();

      const parsedStartTimestamp = Date.parse(start_timestamp);
      const parsedEndTimestamp = Date.parse(end_timestamp);
      const localStartTimestamp = Number.isNaN(parsedStartTimestamp)
        ? getTodayStartTimestamp()
        : Math.floor(parsedStartTimestamp / 1000);
      const localEndTimestamp = Number.isNaN(parsedEndTimestamp)
        ? Math.floor(now.getTime() / 1000 + 3600)
        : Math.floor(parsedEndTimestamp / 1000);

      let url = '';
      if (isAdminUser) {
        url = `/api/operation-log/?p=${startIdx}&page_size=${size}&category=${category}&operator_name=${operator_name}&target_type=${target_type}&target_id=${target_id}&ip=${ip}&success=${success}&start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}`;
      } else {
        url = `/api/operation-log/self?p=${startIdx}&page_size=${size}&category=${category}&success=${success}&start_timestamp=${localStartTimestamp}&end_timestamp=${localEndTimestamp}`;
      }
      url = encodeURI(url);
      const res = await API.get(url);
      const { success: ok, message, data } = res.data;
      if (ok) {
        setActivePage(data.page);
        setPageSize(data.page_size);
        setLogCount(data.total);
        setLogsFormat(data.items);
      } else {
        showError(message);
      }
    } catch (error) {
      showError(error);
    } finally {
      setLoading(false);
    }
  };

  const handlePageChange = (page) => {
    setActivePage(page);
    loadLogs(page, pageSize).then(() => {});
  };

  const handlePageSizeChange = async (size) => {
    localStorage.setItem('page-size', size + '');
    setPageSize(size);
    setActivePage(1);
    await loadLogs(1, size);
  };

  const refresh = async () => {
    setActivePage(1);
    await loadLogs(1, pageSize);
  };

  const copyText = async (e, text) => {
    e.stopPropagation();
    if (await copy(text)) {
      showSuccess(t('已复制：') + text);
    } else {
      Modal.error({ title: t('无法复制到剪贴板，请手动复制'), content: text });
    }
  };

  const hasExpandableRows = () => {
    return logs.some(
      (log) => expandData[log.key] && expandData[log.key].length > 0,
    );
  };

  useEffect(() => {
    const localPageSize =
      parseInt(localStorage.getItem('page-size')) || ITEMS_PER_PAGE;
    setPageSize(localPageSize);
    loadLogs(1, localPageSize)
      .then()
      .catch((reason) => {
        showError(reason);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return {
    logs,
    expandData,
    hasExpandableRows,
    compactMode,
    setCompactMode,
    loading,
    activePage,
    logCount,
    pageSize,
    isAdminUser,
    formApi,
    setFormApi,
    formInitValues,
    getFormValues,
    loadLogs,
    handlePageChange,
    handlePageSizeChange,
    refresh,
    copyText,
    t,
  };
};
