import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import test from 'node:test';
import { installMediaGuard } from '../src/main/media-guard.ts';

class DebuggerFixture extends EventEmitter {
  attached = false;
  events: string[] = [];
  script = '';
  fail = '';
  attach(version: string) {
    this.events.push(`attach:${version}`);
    if (this.fail === 'attach') throw Error('private failure');
    this.attached = true;
  }
  isAttached() {
    return this.attached;
  }
  detach() {
    this.events.push('detach');
    this.attached = false;
    this.emit('detach', undefined, 'target closed');
  }
  async sendCommand(name: string, values?: { source: string }) {
    this.events.push(name);
    if (this.fail === name) throw Error('private failure');
    if (name === 'Page.addScriptToEvaluateOnNewDocument') this.script = values?.source ?? '';
    return name === 'Page.addScriptToEvaluateOnNewDocument' ? { identifier: 'private-script' } : {};
  }
}

test('private debugger registers the main-world guard for every new document before navigation', async () => {
  const debuggerFixture = new DebuggerFixture();
  let detached = 0;
  await installMediaGuard(debuggerFixture, () => {
    detached++;
  });
  assert.deepEqual(debuggerFixture.events, [
    'attach:1.3',
    'Page.enable',
    'Page.addScriptToEvaluateOnNewDocument',
  ]);
  assert.match(debuggerFixture.script, /installDeviceAudioGuard/);
  assert.match(debuggerFixture.script, /getUserMedia/);
  assert.equal(debuggerFixture.isAttached(), true);
  debuggerFixture.detach();
  assert.equal(detached, 1);
});

test('installation failures detach and expose only the fixed public guard failure', async () => {
  for (const failure of ['attach', 'Page.enable', 'Page.addScriptToEvaluateOnNewDocument']) {
    const debuggerFixture = new DebuggerFixture();
    debuggerFixture.fail = failure;
    let detached = 0;
    await assert.rejects(
      installMediaGuard(debuggerFixture, () => {
        detached++;
      }),
      { message: 'GUL_MEDIA_GUARD_UNAVAILABLE' },
    );
    assert.equal(debuggerFixture.attached, false);
    assert.equal(detached, 0);
  }
});

test('detaching or returning no script registration fails closed before loadURL', async () => {
  const debuggerFixture = new DebuggerFixture();
  debuggerFixture.sendCommand = async () => {
    debuggerFixture.attached = false;
    return {};
  };
  await assert.rejects(
    installMediaGuard(debuggerFixture, () => {}),
    { message: 'GUL_MEDIA_GUARD_UNAVAILABLE' },
  );
});
