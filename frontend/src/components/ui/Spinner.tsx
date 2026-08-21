import { CircleNotchIcon } from '@phosphor-icons/react/dist/csr/CircleNotch';
import { cx } from './cx';

export interface SpinnerProps {
  /** Prototype uses 16px inside the Connect button and 15px in the banner. */
  size?: number;
  /** 900ms on the Connect button, 1400ms in the reconnect banner. */
  durationMs?: number;
  className?: string;
  label?: string;
}

/** ph-circle-notch spun by the gul-spin keyframes from base.css. */
export function Spinner({ size = 16, durationMs = 900, className, label = 'Загрузка' }: SpinnerProps) {
  return (
    <CircleNotchIcon
      size={size}
      role="status"
      aria-label={label}
      className={cx('flex-none', className)}
      style={{ animation: `gul-spin ${durationMs}ms linear infinite` }}
    />
  );
}
