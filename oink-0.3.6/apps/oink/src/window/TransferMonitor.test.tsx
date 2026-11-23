// @vitest-environment jsdom
import { StrictMode } from 'react';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { OinkProvider } from 'oink-ui';
import { api } from '../api/client';
import { agent } from '../api/store';
import type { FileTransferStatus } from '../api/types';
import { setLanguage } from '../i18n';
import { TransferMonitor, formatTransferBytes, formatTransferEta, formatTransferSpeed } from './TransferMonitor';

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', { value: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }) });
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
});

afterEach(() => {
  act(() => { agent.reset(); });
  cleanup();
  document.body.innerHTML = '';
  vi.restoreAllMocks();
  setLanguage('en');
});

function mount(language: 'en' | 'zh-Hans', compact = false) {
  setLanguage(language);
  const surface = document.createElement('div');
  document.body.append(surface);
  render(
    <StrictMode>
      <OinkProvider locale={language === 'en' ? 'en-US' : 'zh-CN'} defaultMotion="reduced">
        <TransferMonitor compact={compact} />
      </OinkProvider>
    </StrictMode>,
    { container: surface },
  );
  return surface;
}

describe('TransferMonitor', () => {
  it('formats byte sizes, transfer speeds, and ETAs accurately', () => {
    expect(formatTransferBytes(0)).toBe('0 B');
    expect(formatTransferBytes(512)).toBe('512 B');
    expect(formatTransferBytes(1536)).toBe('1.5 KB');
    expect(formatTransferBytes(50 * 1024 * 1024)).toBe('50.0 MB');
    expect(formatTransferBytes(2 * 1024 * 1024 * 1024)).toBe('2.00 GB');

    expect(formatTransferSpeed(0)).toBeNull();
    expect(formatTransferSpeed(85 * 1024 * 1024)).toBe('85.0 MB/s');
    expect(formatTransferEta(null)).toBeNull();
    expect(formatTransferEta(14)).toBe('14s');
    expect(formatTransferEta(125)).toBe('2m 5s');
  });

  it('renders nothing when no transfers are active', () => {
    const surface = mount('zh-Hans');
    expect(surface.querySelector('.xfer-dock')).toBeNull();
  });

  it('renders live bidirectional transfer progress and cancels on click', async () => {
    const cancelSpy = vi.spyOn(api, 'cancelTransfer').mockResolvedValue(null);
    const listSpy = vi.spyOn(api, 'fileTransfers').mockResolvedValue([]);

    mount('zh-Hans');
    const item: FileTransferStatus = {
      id: 'tx:clip-42',
      direction: 'sending',
      kind: 'drag_and_drop',
      from_name: 'Windows Workstation',
      current_file: 'archive.tar.zst',
      done: 256 * 1024 * 1024,
      total: 512 * 1024 * 1024,
      files_done: 1,
      files: 3,
      speed_bps: 128 * 1024 * 1024,
      eta_secs: 2,
      phase: 'downloading',
      message: null,
    };

    act(() => {
      agent.receive({ type: 'file_transfers', data: [item] });
    });

    expect(screen.getByText('发送中')).toBeTruthy();
    expect(screen.getByText('发送至 Windows Workstation')).toBeTruthy();
    expect(screen.getByText('archive.tar.zst')).toBeTruthy();
    const bar = screen.getByRole('progressbar');
    expect(bar.getAttribute('aria-valuenow')).toBe('50');
    expect(screen.getByText(/256\.0 MB \/ 512\.0 MB \(50%\)/)).toBeTruthy();
    expect(screen.getByText(/1\/3 个文件/)).toBeTruthy();
    expect(screen.getByText(/128\.0 MB\/s/)).toBeTruthy();
    expect(screen.getByText(/剩余 2s/)).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: '取消传输' }));
    await waitFor(() => expect(cancelSpy).toHaveBeenCalledWith('tx:clip-42'));
    await waitFor(() => expect(listSpy).toHaveBeenCalled());
  });
});
