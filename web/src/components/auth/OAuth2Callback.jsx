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

import React, { useContext, useEffect, useRef, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  API,
  showError,
  showSuccess,
  updateAPI,
  setUserData,
} from '../../helpers';
import { UserContext } from '../../context/User';
import { Card, Modal } from '@douyinfe/semi-ui';
import Title from '@douyinfe/semi-ui/lib/es/typography/title';
import Loading from '../common/ui/Loading';
import TwoFAVerification from './TwoFAVerification';

const OAuth2Callback = (props) => {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const [, userDispatch] = useContext(UserContext);
  const navigate = useNavigate();
  const [showTwoFA, setShowTwoFA] = useState(false);

  // 防止 React 18 Strict Mode 下重复执行
  const hasExecuted = useRef(false);

  // 最大重试次数
  const MAX_RETRIES = 3;

  const sendCode = async (code, state, retry = 0) => {
    try {
      const { data: resData } = await API.get(
        `/api/oauth/${props.type}?code=${code}&state=${state}`,
      );

      const { success, message, data } = resData;

      if (!success) {
        showError(message || t('授权失败'));
        return;
      }

      if (data?.require_2fa) {
        setShowTwoFA(true);
        return;
      }

      if (data?.action === 'bind') {
        showSuccess(t('绑定成功！'));
        navigate('/console/personal');
      } else {
        userDispatch({ type: 'login', payload: data });
        localStorage.setItem('user', JSON.stringify(data));
        setUserData(data);
        updateAPI();
        showSuccess(t('登录成功！'));
        navigate('/console/token');
      }
    } catch (error) {
      if (retry < MAX_RETRIES) {
        await new Promise((resolve) => setTimeout(resolve, (retry + 1) * 2000));
        return sendCode(code, state, retry + 1);
      }

      showError(error.message || t('授权失败'));
      navigate('/console/personal');
    }
  };

  const handle2FASuccess = (data) => {
    userDispatch({ type: 'login', payload: data });
    setUserData(data);
    updateAPI();
    showSuccess(t('登录成功！'));
    navigate('/console/token');
  };

  const handleBackToLogin = () => {
    setShowTwoFA(false);
    navigate('/login');
  };

  useEffect(() => {
    if (hasExecuted.current) {
      return;
    }
    hasExecuted.current = true;

    const code = searchParams.get('code');
    const state = searchParams.get('state');

    if (!code) {
      showError(t('未获取到授权码'));
      navigate('/console/personal');
      return;
    }

    sendCode(code, state);
  }, []);

  if (showTwoFA) {
    return (
      <div
        className='relative overflow-hidden bg-gray-100 flex items-center justify-center py-12 px-4 sm:px-6 lg:px-8'
        style={{ minHeight: '60vh' }}
      >
        <div className='w-full max-w-sm'>
          <Card className='border-0 !rounded-2xl overflow-hidden'>
            <div className='flex justify-center pt-6 pb-2'>
              <Title heading={3} className='text-gray-800 dark:text-gray-200'>
                {t('两步验证')}
              </Title>
            </div>
            <div className='px-2 py-4'>
              <TwoFAVerification
                onSuccess={handle2FASuccess}
                onBack={handleBackToLogin}
                isModal={true}
              />
            </div>
          </Card>
        </div>
      </div>
    );
  }

  return <Loading />;
};

export default OAuth2Callback;
