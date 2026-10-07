import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { createElement, type ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import type { ChannelNode, UserInfo } from '../src/shared/contracts.ts';
import type { ChannelList } from '../src/renderer/ChannelList.tsx';

// Compile the real JSX component so regressions cover its displayed SVG and local override.
const compiled = await build({
  stdin: {
    contents: "export { ChannelList } from './ChannelList.tsx';",
    resolveDir: fileURLToPath(new URL('../src/renderer', import.meta.url)),
    loader: 'ts',
  },
  bundle: true,
  platform: 'node',
  format: 'cjs',
  packages: 'external',
  jsx: 'automatic',
  write: false,
});
const module = { exports: {} as { ChannelList: typeof ChannelList } };
new Function('module', 'exports', 'require', compiled.outputFiles[0].text)(
  module,
  module.exports,
  createRequire(import.meta.url),
);
const user: UserInfo = Object.freeze({
  session: 1,
  key: 'voice.1',
  name: 'self',
  channelId: 1,
  selfMute: false,
  selfDeaf: false,
  isSelf: true,
});
function render(audio: ComponentProps<typeof ChannelList>['selfAudio'], member = user): string {
  const tree: ChannelNode = {
    id: member.channelId,
    name: 'channel',
    position: 0,
    children: null,
    users: [member],
  };
  return renderToStaticMarkup(
    createElement(module.exports.ChannelList, {
      tree,
      selected: member.channelId,
      selfSession: 1,
      selfAudio: audio,
      busy: false,
      speakers: [],
      localAudio: {},
      onChannel: () => {},
      onUser: () => {},
    }),
  );
}

test('local mic mute draws a crossed icon immediately despite stale broker state', () => {
  const markup = render({ muted: true, deafened: false });
  assert.equal((markup.match(/class="icon-slash"/gu) ?? []).length, 1);
  assert.match(markup, /role="img" aria-label="Микрофон выключен"/u);
  assert.doesNotMatch(markup, /aria-label="Звук выключен"/u);
  assert.equal(user.selfMute, false);
});

test('local deafen crosses both icons, including when its separate mute flag is false', () => {
  const markup = render({ muted: false, deafened: true });
  assert.equal((markup.match(/class="icon-slash"/gu) ?? []).length, 2);
  assert.match(markup, /role="img" aria-label="Микрофон выключен"/u);
  assert.match(markup, /role="img" aria-label="Звук выключен"/u);
});

test('local unmute removes stale broker badges and a channel move preserves local mute', () => {
  const stale = { ...user, selfMute: true, selfDeaf: true };
  const markup = render({ muted: false, deafened: false }, stale);
  assert.doesNotMatch(markup, /class="icon-slash"|aria-label="(?:Микрофон|Звук) выключен"/u);
  const moved = render({ muted: true, deafened: true }, { ...user, channelId: 2 });
  assert.equal((moved.match(/class="icon-slash"/gu) ?? []).length, 2);
  assert.deepEqual(stale, { ...user, selfMute: true, selfDeaf: true });
});

test('remote users retain their advertised mute and deafen state', () => {
  const remote = { ...user, session: 2, key: 'voice.2', isSelf: false, selfDeaf: true };
  const markup = render({ muted: false, deafened: false }, remote);
  assert.equal((markup.match(/class="icon-slash"/gu) ?? []).length, 2);
  assert.match(markup, /Настройки участника self/u);
});
