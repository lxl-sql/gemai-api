import DOMPurify from 'dompurify';

const SANITIZE_OPTIONS = {
  USE_PROFILES: { html: true },
  FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'form', 'meta', 'link'],
};

export function sanitizeHtmlContent(input) {
  if (typeof input !== 'string' || input.trim() === '') {
    return '';
  }
  return DOMPurify.sanitize(input, SANITIZE_OPTIONS);
}

export function getTrustedHttpsUrl(input) {
  if (typeof input !== 'string' || input.trim() === '') {
    return '';
  }

  try {
    const url = new URL(input.trim());
    return url.protocol === 'https:' ? url.toString() : '';
  } catch {
    return '';
  }
}
