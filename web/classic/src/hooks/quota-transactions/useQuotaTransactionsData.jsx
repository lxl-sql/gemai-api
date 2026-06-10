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

export const useQuotaTransactionsData = () => {
  const { t } = useTranslation();

  const [transactions, setTransactions] = useState([]);
  const [compactMode, setCompactMode] = useTableCompactMode('quota-transactions');
  const [loading, setLoading] = useState(false);
  const [activePage, setActivePage] = useState(1);
  const [totalCount, setTotalCount] = useState(0);
  const [pageSize, setPageSize] = useState(ITEMS_PER_PAGE);
  const [formApi, setFormApi] = useState(null);

  const isAdminUser = isAdmin();
  const now = new Date();

  const formInitValues = {
    dateRange: [
      timestamp2string(getTodayStartTimestamp()),
      timestamp2string(now.getTime() / 1000 + 3600),
    ],
    username: '',
    type: '',
    direction: '',
    bucket: '',
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
      username: formValues.username || '',
      type: formValues.type || '',
      direction: formValues.direction || '',
      bucket: formValues.bucket || '',
      start_timestamp,
      end_timestamp,
    };
  };

  const loadTransactions = async (startIdx, size) => {
    setLoading(true);
    try {
      const {
        username,
        type,
        direction,
        bucket,
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

      const params = new URLSearchParams({
        p: String(startIdx),
        page_size: String(size),
        start_timestamp: String(localStartTimestamp),
        end_timestamp: String(localEndTimestamp),
      });

      if (isAdminUser && username) params.append('username', username);
      if (type) params.append('type', type);
      if (direction) params.append('direction', direction);
      if (bucket) params.append('bucket', bucket);

      const path = isAdminUser
        ? '/api/quota-transactions/'
        : '/api/quota-transactions/self';
      
      const url = `${path}?${params.toString()}`;
      const res = await API.get(url);
      const { success: ok, message, data } = res.data;
      if (ok) {
        setActivePage(data.page);
        setPageSize(data.page_size);
        setTotalCount(data.total);
        setTransactions(data.items || []);
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
    loadTransactions(page, pageSize).then(() => {});
  };

  const handlePageSizeChange = async (size) => {
    localStorage.setItem('page-size', size + '');
    setPageSize(size);
    setActivePage(1);
    await loadTransactions(1, size);
  };

  const refresh = async () => {
    setActivePage(1);
    await loadTransactions(1, pageSize);
  };

  const copyText = async (e, text) => {
    e.stopPropagation();
    if (await copy(text)) {
      showSuccess(t('已复制：') + text);
    } else {
      Modal.error({ title: t('无法复制到剪贴板，请手动复制'), content: text });
    }
  };

  useEffect(() => {
    const localPageSize =
      parseInt(localStorage.getItem('page-size')) || ITEMS_PER_PAGE;
    setPageSize(localPageSize);
    loadTransactions(1, localPageSize)
      .then()
      .catch((reason) => {
        showError(reason);
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return {
    transactions,
    compactMode,
    setCompactMode,
    loading,
    activePage,
    totalCount,
    pageSize,
    isAdminUser,
    formApi,
    setFormApi,
    formInitValues,
    getFormValues,
    loadTransactions,
    handlePageChange,
    handlePageSizeChange,
    refresh,
    copyText,
    t,
  };
};
