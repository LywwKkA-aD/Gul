import { screenSession, sessionGrantProvider, type ScreenGrant } from './session.ts';

export async function openBrowserSession(code: string, request: typeof fetch = fetch) {
  if (!/^[0-9a-f]{64}$/.test(code)) throw new Error('Invalid browser launch');
  const call = async (path: string, token: string, keepalive = false) => {
    try {
      const response = await request(`/api/screen/${path}`, {
        method: path === 'state' ? 'GET' : 'POST',
        headers: { Authorization: `Bearer ${token}` }, credentials: 'omit',
        redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer',
        signal: AbortSignal.timeout(5000), keepalive,
      });
      if (!response.ok) throw new Error();
      return response;
    } catch { throw new Error('Browser screen session ended'); }
  };
  let token = '';
  let closed = false;
  try {
    const response = await call('open', code);
    const data = await response.json();
    const session = screenSession({ state: 'connected', server: data.serverOrigin, epoch: data.epoch, selfChannel: data.channelId });
    if (!session || typeof data.token !== 'string' || !data.token || data.token.length > 256) throw new Error();
    token = data.token;
    const active = () => { if (closed) throw new Error('Browser screen session ended'); };
    const grant = sessionGrantProvider(session, async () => {
      active();
      const reply = await (await call('grant', token)).json() as ScreenGrant;
      active();
      return reply;
    });
    return {
      grant: async () => { active(); return grant(); },
      async check() {
        try {
          active();
          const state = await (await call('state', token)).json();
          if (state.epoch !== session.epoch || state.channelId !== session.channelId) throw new Error();
          active();
        } catch { throw new Error('Browser screen session ended'); }
      },
      async close() {
        if (closed) return;
        closed = true;
        const current = token;
        token = '';
        try { await call('close', current, true); } catch { /* Closing is best effort; the backend owns expiry. */ }
      },
    };
  } catch { throw new Error('Browser screen session ended'); }
}

export type BrowserScreenSession = Awaited<ReturnType<typeof openBrowserSession>>;
