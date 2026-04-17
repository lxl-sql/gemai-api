import { useContext, useEffect, useRef } from 'react';
import { StatusContext } from '../../context/Status';

const MANAGED_SCRIPT_MARKER = 'data-custom-script-managed';

const normalizeDataKey = (key) => {
  if (!key) return '';
  const normalized = String(key).trim().toLowerCase();
  return normalized.startsWith('data-') ? normalized.slice(5) : normalized;
};

const isValidDataKey = (key) => /^[a-z0-9-]{1,64}$/.test(key);

const isValidHttpsUrl = (value) => {
  try {
    const parsed = new URL(value);
    return parsed.protocol === 'https:';
  } catch {
    return false;
  }
};

const parseCustomScriptConfig = (rawConfig) => {
  if (!rawConfig) return [];
  let parsed = rawConfig;
  if (typeof rawConfig === 'string') {
    const trimmed = rawConfig.trim();
    if (!trimmed) return [];
    try {
      parsed = JSON.parse(trimmed);
    } catch {
      return [];
    }
  } else if (typeof rawConfig !== 'object') {
    return [];
  }

  if (!Array.isArray(parsed?.scripts)) {
    return [];
  }

  return parsed.scripts
    .filter((item) => item && typeof item === 'object' && item.src)
    .map((item) => {
      const src = String(item.src).trim();
      if (!isValidHttpsUrl(src)) return null;

      const normalizedData = {};
      if (item.data && typeof item.data === 'object') {
        Object.entries(item.data).forEach(([key, value]) => {
          const normalizedKey = normalizeDataKey(key);
          if (!isValidDataKey(normalizedKey)) return;
          normalizedData[normalizedKey] = String(value ?? '');
        });
      }

      return {
        src,
        id: typeof item.id === 'string' ? item.id.trim() : '',
        async: Boolean(item.async),
        defer: Boolean(item.defer),
        data: normalizedData,
      };
    })
    .filter(Boolean);
};

const toStringKey = (value) => {
  if (!value) return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value);
  } catch {
    return '';
  }
};

const getCustomScriptFromCache = () => {
  const direct = localStorage.getItem('custom_script');
  if (direct) return direct;

  const statusRaw = localStorage.getItem('status');
  if (!statusRaw) return '';
  try {
    const status = JSON.parse(statusRaw);
    return toStringKey(status?.custom_script);
  } catch {
    return '';
  }
};

const injectScripts = (scriptConfigs) => {
  scriptConfigs.forEach((cfg) => {
    const el = document.createElement('script');
    el.src = cfg.src;
    el.type = 'text/javascript';
    if (cfg.id) el.id = cfg.id;
    if (cfg.async) el.async = true;
    if (cfg.defer) el.defer = true;
    el.setAttribute(MANAGED_SCRIPT_MARKER, '1');

    Object.entries(cfg.data).forEach(([key, value]) => {
      el.setAttribute(`data-${key}`, value);
    });

    if (cfg.onLoad) {
      el.addEventListener('load', cfg.onLoad);
    }

    document.body.appendChild(el);
  });
};

const triggerScriptInitializers = () => {
  if (typeof window.embedChatbot === 'function') {
    window.embedChatbot();
  }
};

const removeAllManagedScripts = () => {
  document
    .querySelectorAll(`script[${MANAGED_SCRIPT_MARKER}="1"]`)
    .forEach((el) => el.remove());
};

const removeRelatedElements = () => {
  ['fastgpt-chatbot-button', 'fastgpt-chatbot-window'].forEach((id) => {
    const el = document.getElementById(id);
    if (el) el.remove();
  });
};

const CustomScriptInjector = () => {
  const [statusState] = useContext(StatusContext);
  const appliedKey = useRef(null);
  const initialized = useRef(false);

  useEffect(() => {
    if (initialized.current) return;

    const cached = getCustomScriptFromCache();
    if (!cached) return;

    initialized.current = true;
    appliedKey.current = cached;

    const scripts = parseCustomScriptConfig(cached);
    if (scripts.length > 0) {
      injectScripts(
        scripts.map((s) => ({
          ...s,
          onLoad: triggerScriptInitializers,
        })),
      );
    }
  }, []);

  useEffect(() => {
    const raw = statusState?.status?.custom_script;
    if (raw === undefined || raw === null) return;

    const key = toStringKey(raw);
    if (key === appliedKey.current) return;

    removeAllManagedScripts();
    removeRelatedElements();
    appliedKey.current = key;
    initialized.current = true;

    const scripts = parseCustomScriptConfig(raw);
    if (scripts.length > 0) {
      injectScripts(
        scripts.map((s) => ({
          ...s,
          onLoad: triggerScriptInitializers,
        })),
      );
    }
  }, [statusState?.status?.custom_script]);

  return null;
};

export default CustomScriptInjector;
