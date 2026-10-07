import { createHash, randomBytes } from 'node:crypto';
import { GatewayError } from './errors.ts';

export function validToken(value: string): boolean {
  return !!value && value.length <= 16384 && !/[\r\n]/.test(value);
}
const digest = (value: string) => createHash('sha256').update(value).digest('hex');

export class EpochTokens {
  epoch = 0;
  capability = '';
  private hashes: readonly string[] = [];

  begin(epoch: number): boolean {
    if (!Number.isSafeInteger(epoch) || epoch <= 0 || epoch < this.epoch) throw new GatewayError('stale');
    if (epoch === this.epoch) return false;
    this.epoch = epoch;
    this.capability = randomBytes(32).toString('hex');
    this.hashes = [];
    return true;
  }
  register(epoch: number, value: string): void {
    if (!this.epoch || epoch !== this.epoch) throw new GatewayError('stale');
    if (!validToken(value)) throw new GatewayError('authentication');
    const hash = digest(value);
    if (this.hashes.includes(hash)) return;
    this.hashes = [
      ...(this.hashes.length >= 32 ? [this.hashes[0], ...this.hashes.slice(2)] : this.hashes),
      hash,
    ];
  }
  refresh(epoch: number, previous: string, next: string): void {
    this.register(epoch, next);
    const old = digest(previous);
    this.hashes = this.hashes.filter((hash, index) => index === 0 || hash !== old || hash === digest(next));
  }
  accepts(value: string): boolean {
    return validToken(value) && this.hashes.includes(digest(value));
  }
  clear(): void {
    this.hashes = [];
    this.capability = '';
  }
}
