import { useEffect, useRef, useState } from 'react';
import type { CapturePickerRequest } from '../shared/capture-picker.ts';
import { CapturePickerDialog } from './CapturePicker.tsx';
import './capture-picker.css';

/** The picker receives only local preview DTOs; its reply cannot carry native source IDs. */
export function CapturePickerHost() {
  const [request, setRequest] = useState<CapturePickerRequest | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const current = useRef<CapturePickerRequest | null>(null);
  useEffect(() => {
    const unsubscribe = window.gul.onCapturePicker((next) => {
      current.current = next;
      setRequest(next);
      setBusy(false);
      setError('');
    });
    return () => {
      unsubscribe();
      const pending = current.current;
      current.current = null;
      if (pending) void window.gul.selectCaptureSource(pending.requestId, null).catch(() => {});
    };
  }, []);
  const select = async (sourceKey: string | null) => {
    const pending = current.current;
    if (!pending || busy) return;
    setBusy(true);
    setError('');
    try {
      await window.gul.selectCaptureSource(pending.requestId, sourceKey);
    } catch {
      if (current.current?.requestId === pending.requestId)
        setError('Не удалось выбрать источник. Попробуйте ещё раз.');
    } finally {
      if (current.current?.requestId === pending.requestId) setBusy(false);
    }
  };
  return request ? (
    <CapturePickerDialog
      key={request.requestId}
      request={request}
      busy={busy}
      error={error}
      onSelect={(sourceKey) => void select(sourceKey)}
      onCancel={() => void select(null)}
    />
  ) : null;
}
