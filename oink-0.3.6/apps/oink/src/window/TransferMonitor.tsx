import { useState } from 'react';
import type { CSSProperties } from 'react';
import { Button } from 'oink-ui';
import { api } from '../api/client';
import { agent, reportBackendFailure, useAgent } from '../api/store';
import type { FileTransferStatus } from '../api/types';
import { tr } from '../i18n';

export function formatTransferBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  if (bytes < 1024) return `${Math.round(bytes)} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

export function formatTransferSpeed(bps: number | undefined): string | null {
  if (!bps || !Number.isFinite(bps) || bps <= 0) return null;
  if (bps >= 1024 * 1024 * 1024) return `${(bps / (1024 * 1024 * 1024)).toFixed(2)} GB/s`;
  if (bps >= 1024 * 1024) return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
  return `${Math.max(1, Math.round(bps / 1024))} KB/s`;
}

export function formatTransferEta(secs: number | null | undefined): string | null {
  if (secs == null || !Number.isFinite(secs) || secs < 0) return null;
  const total = Math.max(1, Math.round(secs));
  if (total < 60) return `${total}s`;
  if (total < 3600) return `${Math.floor(total / 60)}m ${total % 60}s`;
  return `${Math.floor(total / 3600)}h ${Math.floor((total % 3600) / 60)}m`;
}

function transferPercent(t: FileTransferStatus): number {
  if (t.phase === 'ready') return 100;
  if (t.total <= 0) return 0;
  return Math.min(100, Math.max(0, Math.round((Math.min(t.done, t.total) / t.total) * 100)));
}

function phaseBadge(t: FileTransferStatus): string {
  switch (t.phase) {
    case 'ready':
      return tr('已完成');
    case 'failed':
      return tr('传输失败');
    case 'cancelled':
      return tr('已取消');
    default:
      return t.direction === 'sending' ? tr('发送中') : tr('接收中');
  }
}

export function TransferMonitor({ compact = false }: { compact?: boolean }) {
  const { transfers } = useAgent();
  const [cancelling, setCancelling] = useState<ReadonlySet<string>>(() => new Set());

  if (!transfers.length) return null;

  const cancel = async (id: string) => {
    setCancelling(prev => new Set([...prev, id]));
    try {
      await api.cancelTransfer(id);
      await agent.query('transfers', async () => (await api.fileTransfers()) ?? []);
    } catch (error) {
      reportBackendFailure(error);
    } finally {
      setCancelling(prev => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    }
  };

  return (
    <section
      className="xfer-dock"
      data-compact={compact ? '' : undefined}
      aria-label={tr('文件传输')}
    >
      {transfers.map(t => {
        const active = t.phase === 'pending' || t.phase === 'downloading';
        const pct = transferPercent(t);
        const peer = t.from_name || tr('对端设备');
        const title = t.direction === 'sending' ? tr('发送至 {0}', peer) : tr('接收自 {0}', peer);
        const speed = active ? formatTransferSpeed(t.speed_bps) : null;
        const eta = active ? formatTransferEta(t.eta_secs) : null;
        const bytesLabel = t.total > 0
          ? `${formatTransferBytes(Math.min(t.done, t.total))} / ${formatTransferBytes(t.total)} (${pct}%)`
          : t.done > 0
            ? formatTransferBytes(t.done)
            : null;
        const metaParts: string[] = [];
        if (t.phase === 'pending' && t.done === 0) metaParts.push(tr('正在准备文件…'));
        if (bytesLabel) metaParts.push(bytesLabel);
        if (t.files > 1) metaParts.push(tr('{0}/{1} 个文件', t.files_done, t.files));
        if (speed) metaParts.push(speed);
        if (eta) metaParts.push(tr('剩余 {0}', eta));
        if (t.phase === 'failed' && t.message) metaParts.push(t.message);

        return (
          <div key={t.id} className="xfer-card" data-phase={t.phase} data-direction={t.direction ?? 'receiving'}>
            <div className="xfer-card__head">
              <div className="xfer-card__titles">
                <div className="xfer-card__title-row">
                  <span className="xfer-card__badge">{phaseBadge(t)}</span>
                  <strong className="xfer-card__peer">{title}</strong>
                </div>
                {t.current_file && <span className="xfer-card__file" title={t.current_file}>{t.current_file}</span>}
              </div>
              {active && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="xfer-card__cancel"
                  loading={cancelling.has(t.id)}
                  aria-label={tr('取消传输')}
                  onClick={() => { void cancel(t.id); }}
                >
                  {tr('取消')}
                </Button>
              )}
            </div>
            <div
              className="xfer-card__bar"
              role="progressbar"
              aria-label={title}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={pct}
            >
              <div className="xfer-card__fill" style={{ '--xfer-pct': `${pct}%` } as CSSProperties} />
            </div>
            {metaParts.length > 0 && (
              <div className="xfer-card__meta">{metaParts.join(' · ')}</div>
            )}
          </div>
        );
      })}
    </section>
  );
}
