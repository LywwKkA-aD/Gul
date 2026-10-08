import { test, expect } from '@playwright/test';
import { SignalResponse } from '@livekit/protocol';
import { WebSocket } from 'ws';
import http from 'node:http';
import { execFile } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { APP_ORIGIN, type BrokerState, type ChannelNode, type MediaGrant } from '../src/shared/contracts.ts';
import { SessionAuthority } from '../src/main/session.ts';
import { parseMemberKey } from '../src/main/member-credentials.ts';
import { createRealityGateway, type Gateway, type GatewayOptions } from '../src/transport/gateway.ts';

const fixture = process.env.GUL_MANAGED_STAND_DIR;
test.skip(!fixture, 'Set GUL_MANAGED_STAND_DIR to a separate managed REALITY fixture.');

function channel(state: BrokerState, name: string): ChannelNode {
  const value = state.tree.children?.find((entry) => entry.name === name);
  if (!value) throw new Error('Managed fixture channel missing.');
  return value;
}
function context(session: Awaited<ReturnType<SessionAuthority['connect']>>) {
  if (!session.serverId) throw new Error('Managed fixture server missing.');
  return { epoch: session.epoch, serverId: session.serverId };
}

async function signal(grant: MediaGrant) {
  const url = new URL(grant.url + '/rtc');
  url.searchParams.set('access_token', grant.token);
  url.searchParams.set('protocol', '16');
  url.searchParams.set('sdk', 'js');
  url.searchParams.set('version', '2.22.3');
  const socket = new WebSocket(url, { origin: APP_ORIGIN });
  let joined = false,
    refreshed: string | undefined;
  let failure = false;
  socket.on('error', () => {
    failure = true;
  });
  socket.on('message', (data, binary) => {
    try {
      const bytes = Buffer.isBuffer(data) ? data : Buffer.from(data as ArrayBuffer);
      const response = binary
        ? SignalResponse.fromBinary(bytes)
        : SignalResponse.fromJsonString(bytes.toString());
      if (response.message.case === 'join') joined = true;
      if (response.message.case === 'refreshToken') refreshed = response.message.value;
    } catch {
      failure = true;
    }
  });
  try {
    await expect.poll(() => joined && !!refreshed && !failure, { timeout: 20_000 }).toBe(true);
    return { socket, refreshed: refreshed! };
  } catch {
    socket.terminate();
    throw new Error('Managed fixture signaling did not join and refresh.');
  }
}

async function validation(gateway: Gateway, token: string): Promise<number> {
  const url = new URL(gateway.signalURL(1, token) + '/rtc/validate');
  url.protocol = 'http:';
  url.searchParams.set('access_token', token);
  return new Promise((finish) => {
    const request = http.get(url, { headers: { Origin: APP_ORIGIN }, timeout: 10_000 }, (response) => {
      response.resume();
      response.once('end', () => finish(response.statusCode ?? 0));
    });
    request.once('timeout', () => request.destroy());
    request.once('error', () => finish(0));
  });
}

async function docker(container: string, args: readonly string[]): Promise<string> {
  if (!/^gul-reality-test-[a-f0-9]+-gateway$/u.test(container))
    throw new Error('Unexpected fixture container.');
  return new Promise((finish, fail) => {
    execFile(
      'docker',
      ['exec', container, ...args],
      { timeout: 20_000, maxBuffer: 4096 },
      (error, stdout) => {
        if (error) fail(new Error('Managed fixture command failed.'));
        else finish(stdout.trim());
      },
    );
  });
}

async function participants(container: string, channelId: number): Promise<number> {
  // Administrative credentials remain inside the private fixture container.
  const script = `
import base64,hashlib,hmac,json,time,urllib.request,sys
cfg=json.load(open('/work/broker.json'))
enc=lambda v:base64.urlsafe_b64encode(json.dumps(v,separators=(',',':')).encode()).decode().rstrip('=')
head=enc({'alg':'HS256','typ':'JWT'})
body=enc({'iss':cfg['apiKey'],'exp':int(time.time())+30,'video':{'roomAdmin':True,'room':'gul-channel-'+sys.argv[1]}})
raw=head+'.'+body
signature=base64.urlsafe_b64encode(hmac.new(cfg['apiSecret'].encode(),raw.encode(),hashlib.sha256).digest()).decode().rstrip('=')
request=urllib.request.Request(cfg['liveKitInternalURL']+'/twirp/livekit.RoomService/ListParticipants',data=json.dumps({'room':'gul-channel-'+sys.argv[1]}).encode(),headers={'Content-Type':'application/json','Authorization':'Bearer '+raw+'.'+signature})
try:
 data=json.load(urllib.request.urlopen(request,timeout=5));print(len(data.get('participants',[])))
except urllib.error.HTTPError as error:
 if error.code==404:print(0)
 else:raise RuntimeError('SFU fixture inspection failed') from None
`;
  const result = await docker(container, ['python3', '-c', script, String(channelId)]);
  if (!/^\d+$/u.test(result)) throw new Error('Invalid fixture participant count.');
  return Number(result);
}

async function restartBroker(container: string) {
  await docker(container, [
    'python3',
    '-c',
    `
import os,signal,time
for entry in os.listdir('/proc'):
 if not entry.isdecimal():continue
 try:args=open('/proc/'+entry+'/cmdline','rb').read().split(b'\\0')
 except OSError:continue
 if args[:1]==[b'/work/broker']:os.kill(int(entry),signal.SIGTERM)
time.sleep(.5)
`,
  ]);
  await docker(container, [
    'sh',
    '-c',
    '/work/broker -config /work/broker.json >/work/broker-restart.log 2>&1 &',
  ]);
}

test('managed REALITY authority revokes actual SFU signaling and refreshed tokens and persists channel permissions', async () => {
  const directory = resolve(fixture!);
  const address = (await readFile(join(directory, 'address'), 'utf8')).trim();
  const password = (await readFile(join(directory, 'join-password'), 'utf8')).trim();
  const ownerKey = parseMemberKey(JSON.parse(await readFile(join(directory, 'owner-key.json'), 'utf8')));
  const names = JSON.parse(await readFile(join(directory, 'containers.json'), 'utf8')) as string[];
  const options: GatewayOptions = {
    address,
    password,
    origin: APP_ORIGIN,
    ca: await readFile(join(directory, 'ca.pem')),
    xrayPath: resolve(
      'resources/xray',
      process.platform + '-' + process.arch,
      process.platform === 'win32' ? 'xray.exe' : 'xray',
    ),
  };
  const authorities: SessionAuthority[] = [],
    gateways: Gateway[] = [],
    sockets: WebSocket[] = [];
  const create = () => {
    const authority = new SessionAuthority(() => createRealityGateway(options));
    authorities.push(authority);
    return authority;
  };
  const owner = create(),
    member = create();
  const suffix = Date.now().toString(36);
  const privateName = 'Managed private test ' + suffix;
  const renamedName = 'Managed renamed test ' + suffix;
  const replacementName = 'Managed replacement test ' + suffix;
  const heartbeat = setInterval(() => {
    void owner.state().catch(() => null);
    void member.state().catch(() => null);
  }, 3000);
  try {
    const ownerSession = await owner.connect(
      { address, password, username: 'managed-owner-fixture' },
      ownerKey,
    );
    expect(ownerSession.member?.role).toBe('owner');
    const ctx = context(ownerSession);
    const ownerFlow = await signal(ownerSession.grant);
    sockets.push(ownerFlow.socket);
    const replay = await createRealityGateway(options);
    gateways.push(replay);
    replay.beginEpoch(1);
    expect(await validation(replay, ownerFlow.refreshed)).toBe(200);
    const claims = JSON.parse(Buffer.from(ownerFlow.refreshed.split('.')[1], 'base64url').toString()) as {
      attributes?: Record<string, string>;
    };
    const original = JSON.parse(
      Buffer.from(ownerSession.grant.token.split('.')[1], 'base64url').toString(),
    ) as { attributes?: Record<string, string> };
    expect(
      ['serverId', 'memberId', 'authVersion', 'sessionNonce', 'revision', 'role'].every(
        (field) =>
          typeof original.attributes?.[field] === 'string' &&
          claims.attributes?.[field] === original.attributes[field],
      ),
    ).toBe(true);

    const guest = await createRealityGateway(options);
    gateways.push(guest);
    const guestLogin = await guest.request<{ sessionToken: string }>('POST', '/api/gul/login', undefined, {
      protocolVersion: 2,
      username: 'managed-owner-fixture',
      password,
    });
    await expect(
      guest.request('POST', '/api/gul/channels/create', guestLogin.sessionToken, {
        name: 'unauthorized',
        access: 'open',
        allowedMemberIds: [],
        catalogVersion: 1,
      }),
    ).rejects.toMatchObject({ code: 'owner-required' });
    await expect(
      guest.request('POST', '/api/gul/login', undefined, { username: 'old-client', password }),
    ).rejects.toMatchObject({ code: 'upgrade-required' });

    const invitation = await owner.createInvitation(ctx);
    const memberKey = await member.redeemInvitation({
      input: { address, password, username: 'managed-member-fixture' },
      inviteToken: invitation.inviteToken,
      rememberIdentity: false,
    });
    const memberSession = await member.connect(
      { address, password, username: 'managed-member-fixture' },
      memberKey,
    );
    expect(memberSession.member?.role).toBe('member');
    const baseline = await owner.state();
    const created = await owner.createChannel({
      ...ctx,
      name: privateName,
      access: 'restricted',
      allowedMemberIds: [memberKey.memberId],
      catalogVersion: baseline!.catalogVersion!,
    });
    const privateChannel = channel(created, privateName);
    const locked = await guest.request<BrokerState>('GET', '/api/gul/state', guestLogin.sessionToken);
    expect(channel(locked, privateName).canJoin).toBe(false);
    expect(channel(locked, privateName).users?.length ?? 0).toBe(0);
    await expect(
      guest.request('POST', '/api/gul/channel', guestLogin.sessionToken, { channelId: privateChannel.id }),
    ).rejects.toMatchObject({ code: 'access-denied' });

    const privateSession = await member.channel(privateChannel.id);
    const voiceFlow = await signal(privateSession.grant);
    sockets.push(voiceFlow.socket);
    const screenGrant = await member.screen({
      channelId: privateSession.channelId,
      revision: privateSession.revision,
    });
    const screenFlow = await signal(screenGrant);
    sockets.push(screenFlow.socket);
    await expect.poll(() => participants(names[1], privateChannel.id)).toBe(2);
    await expect(
      owner.deleteChannel({ ...ctx, channelId: privateChannel.id, version: privateChannel.version! }),
    ).rejects.toThrow('GUL_CHANNEL_BUSY');
    const renamed = await owner.updateChannel({
      ...ctx,
      channelId: privateChannel.id,
      version: privateChannel.version!,
      name: renamedName,
      access: 'restricted',
      allowedMemberIds: [memberKey.memberId],
    });
    expect(ownerFlow.socket.readyState).toBe(WebSocket.OPEN);
    expect(voiceFlow.socket.readyState).toBe(WebSocket.OPEN);
    expect(screenFlow.socket.readyState).toBe(WebSocket.OPEN);
    const next = channel(renamed, renamedName);
    await expect(
      owner.updateChannel({
        ...ctx,
        channelId: next.id,
        version: privateChannel.version!,
        name: 'stale',
        access: 'open',
        allowedMemberIds: [],
      }),
    ).rejects.toThrow('GUL_CATALOG_CONFLICT');
    const revoked = await owner.updateChannel({
      ...ctx,
      channelId: next.id,
      version: next.version!,
      name: next.name,
      access: 'restricted',
      allowedMemberIds: [],
    });
    await expect.poll(() => voiceFlow.socket.readyState).toBe(WebSocket.CLOSED);
    await expect.poll(() => screenFlow.socket.readyState).toBe(WebSocket.CLOSED);
    await expect.poll(() => participants(names[1], next.id)).toBe(0);
    expect(await validation(replay, voiceFlow.refreshed)).toBe(403);
    expect(await validation(replay, screenFlow.refreshed)).toBe(403);
    const policy = await owner.channelPermissions({ ...ctx, channelId: next.id });
    expect(policy.allowedMemberIds.length).toBe(0);
    const finalVersion = channel(revoked, next.name).version!;

    await member.disconnect();
    await owner.disconnect();
    await guest.request('POST', '/api/gul/logout', guestLogin.sessionToken, {});
    await restartBroker(names[1]);
    const restored = await owner.connect({ address, password, username: 'managed-owner-fixture' }, ownerKey);
    const restoredState = await owner.state();
    const persisted = channel(restoredState!, next.name);
    expect(persisted.id).toBe(next.id);
    expect(persisted.version).toBe(finalVersion);
    expect(
      (await owner.channelPermissions({ ...context(restored), channelId: next.id })).allowedMemberIds.length,
    ).toBe(0);
    const restoredMember = await member.connect(
      { address, password, username: 'managed-member-fixture' },
      memberKey,
    );
    expect(restoredMember.member?.id === memberKey.memberId).toBe(true);
    expect(channel((await member.state())!, next.name).canJoin).toBe(false);
    const deleted = await owner.deleteChannel({
      ...context(restored),
      channelId: next.id,
      version: finalVersion,
    });
    expect(deleted.tree.children?.some((value) => value.id === next.id)).toBe(false);
    const replaced = await owner.createChannel({
      ...context(restored),
      name: replacementName,
      access: 'open',
      allowedMemberIds: [],
      catalogVersion: deleted.catalogVersion!,
    });
    const replacement = channel(replaced, replacementName);
    expect(replacement.id).toBeGreaterThan(next.id);
    await owner.deleteChannel({
      ...context(restored),
      channelId: replacement.id,
      version: replacement.version!,
    });
    expect(await validation(replay, voiceFlow.refreshed)).toBe(403);
  } finally {
    clearInterval(heartbeat);
    sockets.forEach((socket) => socket.terminate());
    await Promise.allSettled(authorities.map((authority) => authority.disconnect()));
    await Promise.allSettled(gateways.map((gateway) => gateway.close()));
  }
});
