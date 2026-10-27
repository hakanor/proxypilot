import type { FlowRow } from './api';
import { t } from './i18n';

/** Pill text and color class for a row. */
export function statusPill(r: FlowRow): { label: string; cls: string } {
  if (r.state === 'paused') return { label: t('status.paused'), cls: 'warn' };
  if (r.state === 'failed' || r.error_kind) return { label: r.status ? String(r.status) : t('status.error'), cls: 'err' };
  if (r.state === 'tunneled') return { label: t('status.tunnel'), cls: 'neutral' };
  const s = r.status ?? 0;
  if (!s) return { label: t('status.pending'), cls: 'neutral' };
  if (s >= 500) return { label: String(s), cls: 'err' };
  if (s >= 400) return { label: String(s), cls: 'c4' };
  if (s >= 300 || s === 101) return { label: String(s), cls: 'neutral' };
  return { label: String(s), cls: 'ok' };
}
