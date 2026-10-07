import assert from 'node:assert/strict';
import test from 'node:test';
import { DisplayCaptureConsent } from '../src/main/capture-consent.ts';

test('video-only or unavailable-audio display consent cannot authorize a sound lease', () => {
  const consent = new DisplayCaptureConsent();
  for (const [requested, available] of [
    [false, false],
    [false, true],
    [true, false],
  ]) {
    consent.accept(() => true, requested, available);
    assert.equal(consent.claimAudio(), null);
  }
  consent.accept(() => true);
  assert.equal(consent.claimAudio(), null);
  consent.accept(() => true, true, true);
  assert.ok(consent.claimAudio()?.valid());
});

test('only a recent successful owned display selection grants one audio lease', () => {
  let now = 100;
  let active = true;
  const consent = new DisplayCaptureConsent(() => now);
  assert.equal(consent.claimAudio(), null);
  consent.accept(() => active, true, true);
  const first = consent.claimAudio();
  assert.ok(first);
  assert.equal(consent.claimAudio(), null);
  now += 60000;
  assert.equal(first.valid(), true, 'Accepted running media is not expired by the initial claim deadline');
  active = false;
  assert.equal(first.valid(), false);
});

test('cancel, channel change, expiry and a later request revoke consent', () => {
  let now = 0;
  const consent = new DisplayCaptureConsent(() => now);
  consent.accept(() => true, true, true);
  now = 15001;
  assert.equal(consent.claimAudio(), null);
  consent.accept(() => false, true, true);
  assert.equal(consent.claimAudio(), null);
  consent.accept(() => true, true, true);
  const previous = consent.claimAudio()!;
  consent.invalidate();
  assert.equal(previous.valid(), false);
  assert.equal(consent.claimAudio(), null);
  consent.accept(() => true, true, true);
  const current = consent.claimAudio()!;
  consent.accept(() => true, true, true);
  assert.equal(current.valid(), false);
});

test('a closing frame that throws during validation cannot grant or keep audio', () => {
  const consent = new DisplayCaptureConsent();
  let closing = false;
  consent.accept(
    () => {
      if (closing) throw new Error('Frame closed');
      return true;
    },
    true,
    true,
  );
  const claim = consent.claimAudio()!;
  closing = true;
  assert.equal(claim.valid(), false);
  consent.accept(
    () => {
      throw new Error('Frame closed');
    },
    true,
    true,
  );
  assert.equal(consent.claimAudio(), null);
});

test('canceling a pending picker revokes its request even while the broker epoch is unchanged', async () => {
  const consent = new DisplayCaptureConsent();
  const requestCurrent = consent.beginRequest();
  const brokerEpoch = 7;
  const valid = () => requestCurrent() && brokerEpoch === 7;
  let selected!: () => void;
  const picker = new Promise<void>((resolve) => {
    selected = resolve;
  });
  const pendingSelection = (async () => {
    await picker;
    consent.accept(valid, true, true);
  })();
  consent.invalidate();
  selected();
  await pendingSelection;
  assert.equal(brokerEpoch, 7, 'Native teardown may finish before channel/disconnect changes the epoch');
  assert.equal(valid(), false);
  assert.equal(consent.claimAudio(), null);
});

test('accepting the current picker keeps its request valid, and a later request revokes the running lease', () => {
  const consent = new DisplayCaptureConsent();
  const firstRequest = consent.beginRequest();
  consent.accept(firstRequest, true, true);
  const lease = consent.claimAudio();
  assert.ok(lease);
  assert.ok(lease.valid());
  assert.equal(firstRequest(), true, 'Accepting a source must not invalidate its own frame/request fence');
  const nextRequest = consent.beginRequest();
  assert.equal(firstRequest(), false);
  assert.equal(lease.valid(), false);
  consent.accept(firstRequest, true, true);
  assert.equal(consent.claimAudio(), null, 'A late previous picker cannot authorize the new request');
  consent.accept(nextRequest, true, true);
  assert.ok(consent.claimAudio()?.valid());
});
