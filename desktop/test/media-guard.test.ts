import assert from 'node:assert/strict';
import { createContext, runInContext } from 'node:vm';
import test from 'node:test';
import { installDeviceAudioGuard } from '../src/preload/media-guard.ts';

function fixture(setup = '') {
  const context = createContext({ DOMException });
  runInContext(
    `
    globalThis.calls = [];
    class MediaDevices {
      getUserMedia(value) { calls.push(value); return Promise.resolve({ kind: 'microphone' }); }
      getDisplayMedia() { return Promise.resolve({ kind: 'display' }); }
    }
    class Navigator {
      getUserMedia() { calls.push('legacy'); }
      webkitGetUserMedia() { calls.push('webkit'); }
    }
    globalThis.navigator = new Navigator();
    navigator.mediaDevices = new MediaDevices();
    ${setup}
  `,
    context,
  );
  const installed = runInContext(`(${installDeviceAudioGuard.toString()})()`, context);
  return { context, installed, evaluate: (code: string) => runInContext(code, context) };
}

test('serialized main-world guard permits only cloned device audio while leaving display picker API intact', async () => {
  const { evaluate, installed } = fixture();
  assert.equal(installed, true);
  assert.equal((await evaluate(`navigator.mediaDevices.getUserMedia({audio: true})`)).kind, 'microphone');
  assert.equal((await evaluate(`navigator.mediaDevices.getDisplayMedia({video:true})`)).kind, 'display');
  await evaluate(`navigator.mediaDevices.getUserMedia({audio: {
    deviceId: {exact:'device-id'}, echoCancellation:true, noiseSuppression:{ideal:true},
    autoGainControl:false, channelCount:{ideal:1}, sampleRate:48000, sampleSize:16,
    latency:{min:0,max:1}, voiceIsolation:false
  }, video:false})`);
  assert.equal(evaluate(`calls.length`), 2);
  assert.equal(
    evaluate(`Object.getPrototypeOf(calls[1]) === null && Object.getPrototypeOf(calls[1].audio) === null`),
    true,
  );
});

test('camera, desktop/tab/system constraints and unknown dictionaries never reach native getUserMedia', async () => {
  const { evaluate } = fixture();
  for (const request of [
    `{audio:true,video:true}`,
    `{video:{mandatory:{chromeMediaSource:'desktop'}}}`,
    `{audio:{mandatory:{chromeMediaSource:'system'}}}`,
    `{audio:{optional:[{chromeMediaSource:'desktop'}]}}`,
    `{audio:{chromeMediaSource:'tab'}}`,
    `{audio:{mediaSource:'screen'}}`,
    `{audio:{advanced:[{chromeMediaSource:'desktop'}]}}`,
    `{audio:false}`,
    `{}`,
    `null`,
    `{audio:{deviceId:{exact:'x',mandatory:{chromeMediaSource:'desktop'}}}}`,
    `{audio:{noiseSuppression:'true'}}`,
    `{audio:{sampleRate:Infinity}}`,
    `{audio:{channelCount:-1}}`,
    `{audio:true,unknown:true}`,
  ]) {
    await assert.rejects(evaluate(`navigator.mediaDevices.getUserMedia(${request})`), {
      name: 'NotAllowedError',
    });
  }
  assert.equal(evaluate('calls.length'), 0);
});

test('accessor and prototype tricks cannot introduce legacy constraints during browser conversion', async () => {
  const { evaluate } = fixture();
  await assert.rejects(
    evaluate(`navigator.mediaDevices.getUserMedia({get audio(){ throw Error('getter must not run'); }})`),
    { name: 'NotAllowedError' },
  );
  await assert.rejects(
    evaluate(
      `navigator.mediaDevices.getUserMedia({audio:{get deviceId(){ throw Error('getter must not run'); }}})`,
    ),
    { name: 'NotAllowedError' },
  );
  await assert.rejects(
    evaluate(
      `navigator.mediaDevices.getUserMedia({audio:Object.create({mandatory:{chromeMediaSource:'desktop'}})})`,
    ),
    { name: 'NotAllowedError' },
  );
  await evaluate(`globalThis.original = {audio:{deviceId:{ideal:['a','b']},echoCancellation:true}};
    globalThis.pending = navigator.mediaDevices.getUserMedia(original);
    original.audio.deviceId.ideal[0] = 'changed'; original.audio.mandatory = {chromeMediaSource:'desktop'};
  `);
  assert.equal(evaluate(`calls[0].audio.deviceId.ideal[0]`), 'a');
  assert.equal(evaluate(`'mandatory' in calls[0].audio`), false);
  assert.equal(evaluate('calls.length'), 1);
});

test('device and prototype methods cannot be replaced, rebound to another receiver or bypassed through legacy aliases', async () => {
  const { evaluate } = fixture();
  assert.equal(
    evaluate(`Reflect.set(navigator.mediaDevices,'getUserMedia',()=>Promise.resolve('bypass'))`),
    false,
  );
  assert.equal(evaluate(`Reflect.deleteProperty(MediaDevices.prototype,'getUserMedia')`), false);
  assert.equal(
    evaluate(`Object.getOwnPropertyDescriptor(MediaDevices.prototype,'getUserMedia').configurable`),
    false,
  );
  assert.equal(
    evaluate(`Object.getOwnPropertyDescriptor(navigator.mediaDevices,'getUserMedia').writable`),
    false,
  );
  await assert.rejects(evaluate(`MediaDevices.prototype.getUserMedia.call({}, {audio:true})`), {
    name: 'NotAllowedError',
  });
  for (const method of ['getUserMedia', 'webkitGetUserMedia', 'mozGetUserMedia']) {
    assert.equal(evaluate(`Reflect.set(navigator,'${method}',()=>{})`), false);
    assert.equal(
      evaluate(
        `globalThis.rejected = null; navigator.${method}({video:true},()=>{throw Error('must not capture')},error=>{rejected=error.name}); rejected`,
      ),
      'NotAllowedError',
    );
  }
  assert.equal(evaluate('calls.length'), 0);
});

test('immutable intrinsic captures survive later page monkeypatches and missing capabilities fail closed', async () => {
  const { evaluate } = fixture();
  evaluate(`Object.getOwnPropertyDescriptors = ()=>({}); Reflect.apply = ()=>Promise.resolve('bypass');`);
  await assert.rejects(
    evaluate(`navigator.mediaDevices.getUserMedia({audio:{mandatory:{chromeMediaSource:'system'}}})`),
    { name: 'NotAllowedError' },
  );
  assert.equal((await evaluate(`navigator.mediaDevices.getUserMedia({audio:true})`)).kind, 'microphone');
  assert.equal(fixture(`delete navigator.mediaDevices;`).installed, false);
  assert.equal(
    fixture(`Object.defineProperty(navigator.mediaDevices,'getUserMedia',{value:()=>{},configurable:false});`)
      .installed,
    false,
  );
});

test('new-document and isolated preload installations safely share the sealed guard without wrapping twice', async () => {
  const { evaluate } = fixture();
  evaluate(`globalThis.firstCapture = navigator.mediaDevices.getUserMedia;`);
  assert.equal(evaluate(`(${installDeviceAudioGuard.toString()})()`), true);
  assert.equal(evaluate('navigator.mediaDevices.getUserMedia === firstCapture'), true);
  await evaluate('navigator.mediaDevices.getUserMedia({audio:true})');
  assert.equal(evaluate('calls.length'), 1);
  const marker = evaluate(
    'Object.getOwnPropertyDescriptor(navigator.mediaDevices,"__gulDeviceAudioGuard_v1")',
  );
  assert.equal(marker.configurable, false);
  assert.equal(marker.writable, false);
  assert.equal(Object.isFrozen(marker.value), true);
  assert.equal(
    fixture(
      `Object.defineProperty(navigator.mediaDevices,'__gulDeviceAudioGuard_v1',{value:true,configurable:false});`,
    ).installed,
    false,
  );
});

test('direct host installation covers validation, clone isolation and fixed failures without a test opt-out', async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  const captures: unknown[] = [];
  class Devices {
    getUserMedia(input: unknown) {
      captures.push(input);
      return Promise.resolve({});
    }
  }
  class Nav {
    mediaDevices = new Devices();
  }
  const nav = new Nav();
  Object.defineProperty(globalThis, 'navigator', { value: nav, configurable: true });
  try {
    assert.equal(installDeviceAudioGuard(), true);
    await nav.mediaDevices.getUserMedia({ audio: true });
    await nav.mediaDevices.getUserMedia({ audio: {} });
    await nav.mediaDevices.getUserMedia({
      audio: {
        deviceId: { ideal: ['first', 'second'] },
        groupId: 'device-group',
        latency: { min: 0, max: 1 },
        sampleRate: { exact: 48000 },
        channelCount: 1,
        voiceIsolation: undefined,
        echoCancellation: { ideal: true },
        autoGainControl: false,
      },
    });
    const symbolInput = { audio: true, [Symbol('unexpected')]: true };
    const getter = Object.defineProperty({}, 'audio', {
      get() {
        throw Error('private getter');
      },
    });
    const getterArray = Object.defineProperty([], '0', {
      get() {
        throw Error('private getter');
      },
    });
    for (const input of [
      null,
      [],
      {},
      { audio: false },
      { audio: true, video: true },
      { audio: true, extra: true },
      symbolInput,
      getter,
      { audio: Object.create({ mandatory: { chromeMediaSource: 'desktop' } }) },
      { audio: { mandatory: { chromeMediaSource: 'desktop' } } },
      { audio: { deviceId: '' } },
      { audio: { deviceId: 'x'.repeat(513) } },
      { audio: { deviceId: 'bad\n' } },
      { audio: { deviceId: [] } },
      { audio: { deviceId: [1] } },
      { audio: { deviceId: getterArray } },
      { audio: { deviceId: [''] } },
      { audio: { deviceId: { min: 'device' } } },
      { audio: { deviceId: {} } },
      { audio: { echoCancellation: 'true' } },
      { audio: { latency: Infinity } },
      { audio: { sampleRate: 1.5 } },
      { audio: { channelCount: 0 } },
      { audio: { sampleSize: 65 } },
      { audio: { latency: { min: 1, max: 0 } } },
      { audio: { noiseSuppression: null } },
    ])
      await assert.rejects(nav.mediaDevices.getUserMedia(input), { name: 'NotAllowedError' });
    assert.equal(captures.length, 3);
    assert.equal(installDeviceAudioGuard(), true);
    const legacy = (nav as unknown as Record<string, (...args: unknown[]) => void>).getUserMedia;
    assert.throws(() => legacy({}, () => {}), { name: 'NotAllowedError' });
    let errorName = '';
    legacy(
      {},
      () => {},
      (error: DOMException) => {
        errorName = error.name;
      },
    );
    assert.equal(errorName, 'NotAllowedError');
    const capture = nav.mediaDevices.getUserMedia;
    await assert.rejects(capture.call({} as Devices, { audio: true }), { name: 'NotAllowedError' });
  } finally {
    if (previous) Object.defineProperty(globalThis, 'navigator', previous);
    else Reflect.deleteProperty(globalThis, 'navigator');
  }
});
