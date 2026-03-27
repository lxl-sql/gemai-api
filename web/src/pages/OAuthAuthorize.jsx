import React, { useEffect, useState } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import {
  Button,
  Card,
  Typography,
  Avatar,
  Spin,
  Divider,
} from '@douyinfe/semi-ui';
import {
  IconCheckCircleStroked,
  IconMailStroked,
  IconUserStroked,
  IconClose,
  IconKey,
} from '@douyinfe/semi-icons';
import { API, showError, getLogo, getSystemName } from '../helpers';
import { useTranslation } from 'react-i18next';

const { Title, Text, Paragraph } = Typography;

const OAuthAuthorize = () => {
  const { t } = useTranslation();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();

  const [loading, setLoading] = useState(true);
  const [approving, setApproving] = useState(false);
  const [appInfo, setAppInfo] = useState(null);
  const [error, setError] = useState('');

  const clientId = searchParams.get('client_id') || '';
  const redirectUri = searchParams.get('redirect_uri') || '';
  const scope = searchParams.get('scope') || 'profile';
  const state = searchParams.get('state') || '';
  const responseType = searchParams.get('response_type') || 'code';

  const logo = getLogo();
  const systemName = getSystemName() || 'API';

  useEffect(() => {
    if (!clientId || !redirectUri) {
      setError(t('缺少必要参数：client_id 或 redirect_uri'));
      setLoading(false);
      return;
    }
    if (responseType !== 'code') {
      setError(t('仅支持 response_type=code'));
      setLoading(false);
      return;
    }
    fetchAppInfo();

    const handlePageShow = (e) => {
      if (e.persisted) {
        setApproving(false);
        fetchAppInfo();
      }
    };
    window.addEventListener('pageshow', handlePageShow);
    return () => window.removeEventListener('pageshow', handlePageShow);
  }, []);

  const fetchAppInfo = async () => {
    try {
      const res = await API.get('/api/oauth-server/authorize', {
        params: { client_id: clientId, redirect_uri: redirectUri, scope },
      });
      const { success, message, data } = res.data;
      if (!success) {
        setError(message);
        setLoading(false);
        return;
      }
      if (!data.logged_in) {
        const returnUrl = window.location.pathname + window.location.search;
        navigate('/login?redirect=' + encodeURIComponent(returnUrl));
        return;
      }
      setAppInfo(data);
    } catch (err) {
      if (err.response && err.response.status === 401) {
        const returnUrl = window.location.pathname + window.location.search;
        navigate('/login?redirect=' + encodeURIComponent(returnUrl));
        return;
      }
      setError(t('无法加载应用信息'));
    } finally {
      setLoading(false);
    }
  };

  const handleApprove = async () => {
    setApproving(true);
    try {
      const res = await API.post('/api/oauth-server/authorize', {
        client_id: clientId,
        redirect_uri: redirectUri,
        scope,
        state,
        csrf_token: appInfo?.csrf_token || '',
      });
      const { success, message, data } = res.data;
      if (!success) {
        showError(message);
        setApproving(false);
        return;
      }
      window.location.href = data.redirect_url;
    } catch (err) {
      showError(t('授权失败'));
      setApproving(false);
    }
  };

  const handleDeny = () => {
    let denyUrl = redirectUri;
    if (denyUrl.includes('?')) {
      denyUrl += '&';
    } else {
      denyUrl += '?';
    }
    denyUrl += 'error=access_denied&error_description=user_denied';
    if (state) {
      denyUrl += '&state=' + state;
    }
    window.location.href = denyUrl;
  };

  const scopeList = scope.split(' ').filter(Boolean);

  const scopeDescriptions = {
    profile: { icon: <IconUserStroked style={{ fontSize: 16 }} />, label: t('用户名和个人资料') },
    email: { icon: <IconMailStroked style={{ fontSize: 16 }} />, label: t('邮箱地址') },
    api: { icon: <IconKey style={{ fontSize: 16 }} />, label: t('API 接口访问权限') },
  };

  if (loading) {
    return (
      <div className='relative overflow-hidden bg-gray-100 flex items-center justify-center min-h-screen px-4'>
        <div className='blur-ball blur-ball-indigo' style={{ top: '-80px', right: '-80px', transform: 'none' }} />
        <div className='blur-ball blur-ball-teal' style={{ top: '50%', left: '-120px' }} />
        <Spin size='large' />
      </div>
    );
  }

  if (error) {
    return (
      <div className='relative overflow-hidden bg-gray-100 flex items-center justify-center min-h-screen px-4'>
        <div className='blur-ball blur-ball-indigo' style={{ top: '-80px', right: '-80px', transform: 'none' }} />
        <div className='blur-ball blur-ball-teal' style={{ top: '50%', left: '-120px' }} />
        <div className='w-full max-w-sm'>
          <div className='flex items-center justify-center mb-6 gap-2'>
            {logo && <img src={logo} alt='Logo' className='h-10 rounded-full' />}
            <Title heading={3} className='!text-gray-800'>{systemName}</Title>
          </div>
          <Card className='border-0 !rounded-2xl overflow-hidden'>
            <div className='flex flex-col items-center py-8 px-4'>
              <div className='w-16 h-16 rounded-full bg-red-50 flex items-center justify-center mb-4'>
                <IconClose style={{ fontSize: 28, color: 'var(--semi-color-danger)' }} />
              </div>
              <Title heading={4} className='!text-gray-800'>{t('授权失败')}</Title>
              <Paragraph type='tertiary' className='mt-3 text-center'>{error}</Paragraph>
              <Button
                theme='solid'
                type='primary'
                className='w-full h-12 !rounded-full mt-6 bg-black text-white hover:bg-gray-800 transition-colors'
                onClick={() => navigate('/')}
              >
                {t('返回首页')}
              </Button>
            </div>
          </Card>
        </div>
      </div>
    );
  }

  return (
    <div className='relative overflow-hidden bg-gray-100 flex items-center justify-center min-h-screen py-12 px-4'>
      <div className='blur-ball blur-ball-indigo' style={{ top: '-80px', right: '-80px', transform: 'none' }} />
      <div className='blur-ball blur-ball-teal' style={{ top: '50%', left: '-120px' }} />

      <div className='w-full max-w-sm'>
        <div className='flex items-center justify-center mb-6 gap-2'>
          {logo && <img src={logo} alt='Logo' className='h-10 rounded-full' />}
          <Title heading={3} className='!text-gray-800'>{systemName}</Title>
        </div>

        <Card className='border-0 !rounded-2xl overflow-hidden'>
          {/* Header */}
          <div className='flex justify-center pt-6 pb-2'>
            <Title heading={3} className='text-gray-800 dark:text-gray-200'>
              {t('授权登录')}
            </Title>
          </div>

          <div className='px-4 pb-6'>
            {/* App info */}
            <div className='flex flex-col items-center py-4'>
              <div className='flex items-center gap-4 mb-3'>
                {appInfo.app_logo ? (
                  <Avatar size='large' src={appInfo.app_logo} shape='circle' />
                ) : (
                  <Avatar
                    size='large'
                    shape='circle'
                    className='bg-blue-500 text-white text-xl font-bold'
                  >
                    {appInfo.app_name?.charAt(0)?.toUpperCase()}
                  </Avatar>
                )}
                <div className='flex items-center gap-1 text-gray-300'>
                  <div className='w-1.5 h-1.5 rounded-full bg-gray-300' />
                  <div className='w-1.5 h-1.5 rounded-full bg-gray-400' />
                  <div className='w-1.5 h-1.5 rounded-full bg-gray-500' />
                </div>
                {logo ? (
                  <Avatar size='large' src={logo} shape='circle' />
                ) : (
                  <Avatar
                    size='large'
                    shape='circle'
                    className='bg-gray-700 text-white text-xl font-bold'
                  >
                    {systemName.charAt(0)?.toUpperCase()}
                  </Avatar>
                )}
              </div>
              <Text strong className='text-base'>
                {appInfo.app_name}
              </Text>
              {appInfo.app_description && (
                <Text type='tertiary' size='small' className='mt-1'>
                  {appInfo.app_description}
                </Text>
              )}
            </div>

            <Divider margin='12px' />

            {/* Scope list */}
            <div className='py-3'>
              <Text type='tertiary' size='small' className='block mb-3'>
                {appInfo.app_name} {t('将获取以下信息')}：
              </Text>
              <div className='space-y-2'>
                {scopeList.map((s) => {
                  const desc = scopeDescriptions[s];
                  if (!desc) return null;
                  return (
                    <div
                      key={s}
                      className='flex items-center gap-3 px-4 py-3 rounded-xl bg-gray-50 dark:bg-gray-800'
                    >
                      <IconCheckCircleStroked style={{ color: 'var(--semi-color-success)', fontSize: 18, flexShrink: 0 }} />
                      <span className='flex items-center gap-2 text-sm'>
                        {desc.icon}
                        {desc.label}
                      </span>
                    </div>
                  );
                })}
              </div>
            </div>

            <Text type='tertiary' size='small' className='block mt-2 mb-5 leading-relaxed'>
              {t('授权后，')}{appInfo.app_name}{' '}
              {t('将能够读取您选定范围内的信息。您可以随时撤销此授权。')}
            </Text>

            {/* Actions */}
            <div className='space-y-3'>
              <Button
                theme='solid'
                type='primary'
                className='w-full h-12 !rounded-full bg-black text-white hover:bg-gray-800 transition-colors'
                onClick={handleApprove}
                loading={approving}
              >
                {t('允许授权')}
              </Button>
              <Button
                theme='outline'
                type='tertiary'
                className='w-full h-12 !rounded-full border border-gray-200 hover:bg-gray-50 transition-colors'
                onClick={handleDeny}
                disabled={approving}
              >
                {t('取消')}
              </Button>
            </div>
          </div>
        </Card>
      </div>
    </div>
  );
};

export default OAuthAuthorize;
