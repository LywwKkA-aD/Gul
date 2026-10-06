import type { JoinGrant } from './controller';

export async function localGrant(identity: string): Promise<JoinGrant> {
  if (window.location.hash === '#livekit-native') {
    const { ScreenShareLabService } = await import('../../bindings/github.com/LywwKkA-aD/Gul/services');
    return ScreenShareLabService.Join(identity);
  }
  const response = await fetch('/api/livekit/token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ identity, room: 'gul-local' }),
    credentials: 'omit',
    cache: 'no-store',
    signal: AbortSignal.timeout(5000),
  });
  if (!response.ok) throw new Error('Local room unavailable');
  const grant: JoinGrant = await response.json();
  if (grant.url !== 'ws://127.0.0.1:7880' || grant.room !== 'gul-local' || grant.identity !== identity || !grant.token) {
    throw new Error('Invalid local room');
  }
  return grant;
}
