import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Button,
  Typography,
  Modal,
  SideSheet,
  Form,
  Space,
  Tag,
  Toast,
  Banner,
  Empty,
} from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { IconCopy, IconSearch, IconSave, IconClose } from '@douyinfe/semi-icons';
import { ShieldCheck } from 'lucide-react';
import CardPro from '../components/common/ui/CardPro';
import CardTable from '../components/common/ui/CardTable';
import { API, showError, showSuccess } from '../helpers';
import { useIsMobile } from '../hooks/common/useIsMobile';
import CompactModeToggle from '../components/common/ui/CompactModeToggle';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

const OAuthApps = () => {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  const [apps, setApps] = useState([]);
  const [loading, setLoading] = useState(true);
  const [createVisible, setCreateVisible] = useState(false);
  const [editVisible, setEditVisible] = useState(false);
  const [editingApp, setEditingApp] = useState(null);
  const [newSecret, setNewSecret] = useState(null);
  const [submitting, setSubmitting] = useState(false);
  const [deleteVisible, setDeleteVisible] = useState(false);
  const [resetVisible, setResetVisible] = useState(false);
  const [targetApp, setTargetApp] = useState(null);
  const [compactMode, setCompactMode] = useState(false);
  const searchFormRef = useRef(null);
  const currentKeyword = useRef('');

  const loadApps = useCallback(async (keyword) => {
    if (keyword !== undefined) {
      currentKeyword.current = keyword;
    }
    setLoading(true);
    try {
      const params = currentKeyword.current ? { keyword: currentKeyword.current } : {};
      const res = await API.get('/api/oauth-app/', { params });
      const { success, data } = res.data;
      if (success) {
        setApps(data || []);
      }
    } catch (err) {
      showError(t('加载失败'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    loadApps('');
  }, [loadApps]);

  const handleSearch = (values) => {
    loadApps(values?.keyword || '');
  };

  const handleSearchReset = () => {
    searchFormRef.current?.reset();
    loadApps('');
  };

  const handleCreate = async (values) => {
    setSubmitting(true);
    try {
      const res = await API.post('/api/oauth-app/', {
        name: values.name,
        description: values.description || '',
        logo: values.logo || '',
        redirect_uris: values.redirect_uris || [],
      });
      const { success, message, data } = res.data;
      if (success) {
        setNewSecret(data);
        setCreateVisible(false);
        showSuccess(t('创建成功'));
        loadApps();
      } else {
        showError(message);
      }
    } catch (err) {
      showError(t('创建失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const handleUpdate = async (values) => {
    if (!editingApp) return;
    setSubmitting(true);
    try {
      const res = await API.put(`/api/oauth-app/${editingApp.id}`, {
        name: values.name,
        description: values.description || '',
        logo: values.logo || '',
        redirect_uris: values.redirect_uris || [],
      });
      const { success, message } = res.data;
      if (success) {
        setEditVisible(false);
        setEditingApp(null);
        showSuccess(t('更新成功'));
        loadApps();
      } else {
        showError(message);
      }
    } catch (err) {
      showError(t('更新失败'));
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!targetApp) return;
    try {
      const res = await API.delete(`/api/oauth-app/${targetApp.id}`);
      if (res.data.success) {
        showSuccess(t('删除成功'));
        loadApps();
      } else {
        showError(res.data.message);
      }
    } catch (err) {
      showError(t('删除失败'));
    } finally {
      setDeleteVisible(false);
      setTargetApp(null);
    }
  };

  const handleResetSecret = async () => {
    if (!targetApp) return;
    try {
      const res = await API.post(`/api/oauth-app/${targetApp.id}/reset-secret`);
      const { success, message, data } = res.data;
      if (success) {
        setNewSecret({
          client_id: targetApp.client_id,
          client_secret: data.client_secret,
        });
        showSuccess(t('密钥已重置'));
        loadApps();
      } else {
        showError(message);
      }
    } catch (err) {
      showError(t('重置失败'));
    } finally {
      setResetVisible(false);
      setTargetApp(null);
    }
  };

  const copyText = (text) => {
    navigator.clipboard.writeText(text).then(() => {
      Toast.success(t('已复制到剪切板'));
    });
  };

  const columns = [
    {
      title: t('名称'),
      dataIndex: 'name',
      key: 'name',
      width: 150,
    },
    {
      title: 'Client ID',
      dataIndex: 'client_id',
      key: 'client_id',
      render: (text) => (
        <Space>
          <Text style={{ fontFamily: 'monospace', fontSize: 12 }}>{text}</Text>
          <Button
            icon={<IconCopy />}
            size='small'
            theme='borderless'
            onClick={() => copyText(text)}
          />
        </Space>
      ),
    },
    {
      title: t('回调地址'),
      dataIndex: 'redirect_uris',
      key: 'redirect_uris',
      render: (text) => {
        try {
          const uris = JSON.parse(text || '[]');
          return (
            <Space wrap>
              {uris.map((uri, i) => (
                <Tag key={i} size='small'>{uri}</Tag>
              ))}
            </Space>
          );
        } catch {
          return '-';
        }
      },
    },
    {
      title: t('状态'),
      dataIndex: 'status',
      key: 'status',
      width: 80,
      render: (status) => (
        <Tag color={status === 1 ? 'green' : 'red'} shape='circle' size='small'>
          {status === 1 ? t('启用') : t('禁用')}
        </Tag>
      ),
    },
    {
      title: '',
      dataIndex: 'operate',
      key: 'operate',
      fixed: 'right',
      width: 200,
      render: (_, record) => (
        <Space>
          <Button
            type='tertiary'
            size='small'
            onClick={() => {
              setEditingApp(record);
              setEditVisible(true);
            }}
          >
            {t('编辑')}
          </Button>
          <Button
            type='warning'
            size='small'
            onClick={() => {
              setTargetApp(record);
              setResetVisible(true);
            }}
          >
            {t('重置密钥')}
          </Button>
          <Button
            type='danger'
            size='small'
            onClick={() => {
              setTargetApp(record);
              setDeleteVisible(true);
            }}
          >
            {t('删除')}
          </Button>
        </Space>
      ),
    },
  ];

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

  return (
    <div className='mt-[60px] px-2'>
      <CardPro
        type='type1'
        descriptionArea={
          <div className='flex flex-col md:flex-row justify-between items-start md:items-center gap-2 w-full'>
            <div className='flex items-center text-blue-500'>
              <ShieldCheck size={16} className='mr-2' />
              <Text>{t('OAuth 应用管理')}</Text>
            </div>
            <CompactModeToggle
              compactMode={compactMode}
              setCompactMode={setCompactMode}
              t={t}
            />
          </div>
        }
        actionsArea={
          <div className='flex flex-col md:flex-row justify-between items-center gap-2 w-full'>
            <div className='flex flex-wrap gap-2 w-full md:w-auto order-2 md:order-1'>
              <Button
                type='primary'
                className='flex-1 md:flex-initial'
                onClick={() => setCreateVisible(true)}
                size='small'
              >
                {t('创建应用')}
              </Button>
            </div>

            <Form
              layout='horizontal'
              getFormApi={(api) => { searchFormRef.current = api; }}
              onSubmit={handleSearch}
              allowEmpty
              autoComplete='off'
              className='w-full md:w-auto order-1 md:order-2'
            >
              <div className='flex flex-col md:flex-row items-center gap-2 w-full md:w-auto'>
                <div className='relative w-full md:w-56'>
                  <Form.Input
                    field='keyword'
                    prefix={<IconSearch />}
                    placeholder={t('搜索应用名称或 Client ID')}
                    showClear
                    pure
                    size='small'
                  />
                </div>
                <div className='flex gap-2 w-full md:w-auto'>
                  <Button
                    type='tertiary'
                    htmlType='submit'
                    loading={loading}
                    className='flex-1 md:flex-initial'
                    size='small'
                  >
                    {t('查询')}
                  </Button>
                  <Button
                    type='tertiary'
                    className='flex-1 md:flex-initial'
                    size='small'
                    onClick={handleSearchReset}
                  >
                    {t('重置')}
                  </Button>
                </div>
              </div>
            </Form>
          </div>
        }
        t={t}
      >
        <CardTable
          columns={tableColumns}
          dataSource={apps}
          loading={loading}
          rowKey='id'
          scroll={compactMode ? undefined : { x: 'max-content' }}
          hidePagination
          empty={
            <Empty
              image={<IllustrationNoResult style={{ width: 150, height: 150 }} />}
              darkModeImage={<IllustrationNoResultDark style={{ width: 150, height: 150 }} />}
              description={t('暂无应用')}
              style={{ padding: 30 }}
            />
          }
          className='rounded-xl overflow-hidden'
          size='middle'
        />
      </CardPro>

      {/* Secret display */}
      <SideSheet
        placement='right'
        title={
          <Space>
            <Tag color='orange' shape='circle'>{t('重要')}</Tag>
            <Text strong>{t('应用凭据')}</Text>
          </Space>
        }
        visible={!!newSecret}
        onCancel={() => setNewSecret(null)}
        width={isMobile ? '100%' : 500}
        closable={false}
        maskClosable={false}
        bodyStyle={{ padding: '16px' }}
        footer={
          <div className='flex justify-end'>
            <Button theme='solid' onClick={() => setNewSecret(null)}>
              {t('我已保存，关闭')}
            </Button>
          </div>
        }
      >
        <Banner
          type='warning'
          description={t('Client Secret 仅显示一次，请立即保存。关闭后将无法再次查看。')}
          style={{ marginBottom: 16 }}
        />
        {newSecret && (
          <div className='flex flex-col gap-4'>
            <div>
              <div className='flex items-center justify-between mb-1'>
                <Text strong>Client ID</Text>
                <Button
                  icon={<IconCopy />}
                  size='small'
                  theme='borderless'
                  onClick={() => copyText(newSecret.client_id)}
                >
                  {t('复制')}
                </Button>
              </div>
              <code
                className='block w-full break-all p-3 rounded-lg text-sm'
                style={{ background: 'var(--semi-color-bg-1)', border: '1px solid var(--semi-color-border)' }}
              >
                {newSecret.client_id}
              </code>
            </div>
            <div>
              <div className='flex items-center justify-between mb-1'>
                <Text strong>Client Secret</Text>
                <Button
                  icon={<IconCopy />}
                  size='small'
                  theme='borderless'
                  onClick={() => copyText(newSecret.client_secret)}
                >
                  {t('复制')}
                </Button>
              </div>
              <code
                className='block w-full break-all p-3 rounded-lg text-sm'
                style={{ background: 'var(--semi-color-bg-1)', border: '1px solid var(--semi-color-border)' }}
              >
                {newSecret.client_secret}
              </code>
            </div>
          </div>
        )}
      </SideSheet>

      {/* Create SideSheet */}
      <SideSheet
        placement='left'
        title={
          <Space>
            <Tag color='green' shape='circle'>{t('新建')}</Tag>
            <Text strong>{t('创建 OAuth 应用')}</Text>
          </Space>
        }
        visible={createVisible}
        onCancel={() => setCreateVisible(false)}
        width={isMobile ? '100%' : 500}
        bodyStyle={{ padding: '16px' }}
        footer={null}
      >
        <Form onSubmit={handleCreate}>
          <Form.Input
            field='name'
            label={t('应用名称')}
            placeholder={t('我的应用')}
            rules={[{ required: true, message: t('请输入应用名称') }]}
            style={{ width: '100%' }}
          />
          <Form.Input
            field='description'
            label={t('应用描述')}
            placeholder={t('可选')}
            style={{ width: '100%' }}
          />
          <Form.Input
            field='logo'
            label={t('Logo URL')}
            placeholder='https://example.com/logo.png'
            style={{ width: '100%' }}
          />
          <Form.TagInput
            field='redirect_uris'
            label={t('回调地址')}
            placeholder={t('输入后按回车添加')}
            rules={[{ required: true, message: t('请至少添加一个回调地址') }]}
            style={{ width: '100%' }}
          />
          <div className='flex justify-end mt-4'>
            <Space>
              <Button
                theme='solid'
                type='primary'
                htmlType='submit'
                loading={submitting}
                icon={<IconSave />}
              >
                {t('创建')}
              </Button>
              <Button icon={<IconClose />} onClick={() => setCreateVisible(false)}>
                {t('取消')}
              </Button>
            </Space>
          </div>
        </Form>
      </SideSheet>

      {/* Edit SideSheet */}
      <SideSheet
        placement='right'
        title={
          <Space>
            <Tag color='blue' shape='circle'>{t('编辑')}</Tag>
            <Text strong>{editingApp?.name || t('编辑 OAuth 应用')}</Text>
          </Space>
        }
        visible={editVisible}
        onCancel={() => { setEditVisible(false); setEditingApp(null); }}
        width={isMobile ? '100%' : 500}
        bodyStyle={{ padding: '16px' }}
        footer={null}
      >
        {editingApp && (
          <Form
            onSubmit={handleUpdate}
            initValues={{
              name: editingApp.name,
              description: editingApp.description,
              logo: editingApp.logo,
              redirect_uris: (() => {
                try { return JSON.parse(editingApp.redirect_uris || '[]'); }
                catch { return []; }
              })(),
            }}
          >
            <Form.Input
              field='name'
              label={t('应用名称')}
              rules={[{ required: true, message: t('请输入应用名称') }]}
              style={{ width: '100%' }}
            />
            <Form.Input field='description' label={t('应用描述')} style={{ width: '100%' }} />
            <Form.Input field='logo' label={t('Logo URL')} style={{ width: '100%' }} />
            <Form.TagInput
              field='redirect_uris'
              label={t('回调地址')}
              placeholder={t('输入后按回车添加')}
              style={{ width: '100%' }}
            />
            <div className='flex justify-end mt-4'>
              <Space>
                <Button
                  theme='solid'
                  type='primary'
                  htmlType='submit'
                  loading={submitting}
                  icon={<IconSave />}
                >
                  {t('保存')}
                </Button>
                <Button
                  icon={<IconClose />}
                  onClick={() => { setEditVisible(false); setEditingApp(null); }}
                >
                  {t('取消')}
                </Button>
              </Space>
            </div>
          </Form>
        )}
      </SideSheet>

      {/* Delete confirmation */}
      <Modal
        title={t('确定要删除此应用吗？')}
        visible={deleteVisible}
        onCancel={() => { setDeleteVisible(false); setTargetApp(null); }}
        onOk={handleDelete}
        type='danger'
      >
        {t('删除后该应用的所有授权将失效，此操作不可撤销。')}
      </Modal>

      {/* Reset secret confirmation */}
      <Modal
        title={t('确定要重置密钥吗？')}
        visible={resetVisible}
        onCancel={() => { setResetVisible(false); setTargetApp(null); }}
        onOk={handleResetSecret}
        type='warning'
      >
        {t('旧密钥将立即失效，使用旧密钥的应用需要更新配置。')}
      </Modal>
    </div>
  );
};

export default OAuthApps;
