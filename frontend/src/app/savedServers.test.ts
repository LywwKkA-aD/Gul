import test from 'node:test';
import assert from 'node:assert/strict';
import { connectFallback, savedServerRows } from './savedServers.ts';

// ─── rows ────────────────────────────────────────────────────────────────────

test('a remembered server becomes a row with a readable label', () => {
  const rows = savedServerRows([
    { address: 'murmur.example.com:64738', username: 'gul', hasPassword: true },
  ]);
  assert.equal(rows.length, 1);
  assert.deepEqual(rows[0], {
    address: 'murmur.example.com:64738',
    username: 'gul',
    hasPassword: true,
    label: 'murmur.example.com',
    hint: 'пароль сохранён',
  });
});

// The address is the key the password is stored under, so the row keeps it
// exactly as it was remembered while the label is free to be readable.
test('the label is a picture of the address, never a replacement for it', () => {
  const [row] = savedServerRows([
    { address: 'wss://relay.example.com/mumble', username: 'gul', hasPassword: false },
  ]);
  assert.equal(row.address, 'wss://relay.example.com/mumble');
  assert.equal(row.label, 'relay.example.com');
});

test('a server without a stored password says nothing extra', () => {
  const [row] = savedServerRows([{ address: 'a.example:64738', username: 'gul' }]);
  assert.equal(row.hasPassword, false);
  assert.equal(row.hint, '');
});

test('order is the backend order: newest first, untouched', () => {
  const rows = savedServerRows([
    { address: 'new.example:64738', username: 'gul' },
    { address: 'old.example:64738', username: 'gul' },
  ]);
  assert.deepEqual(
    rows.map((r) => r.address),
    ['new.example:64738', 'old.example:64738'],
  );
});

test('an entry that could not be dialled is not a row', () => {
  const rows = savedServerRows([
    { address: '   ', username: 'gul' },
    { address: 'a.example:64738', username: '  ' },
    { address: 'a.example:64738' },
    { username: 'gul' },
    null,
    'a.example:64738',
    42,
  ]);
  assert.deepEqual(rows, []);
});

test('one row per address', () => {
  const rows = savedServerRows([
    { address: 'a.example:64738', username: 'first', hasPassword: true },
    { address: 'a.example:64738', username: 'second' },
  ]);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].username, 'first');
});

test('whitespace never reaches a row', () => {
  const [row] = savedServerRows([{ address: '  a.example:64738 ', username: '  Аня ' }]);
  assert.equal(row.address, 'a.example:64738');
  assert.equal(row.username, 'Аня');
});

// A call that failed, or bindings from another build: the picker shows nothing
// rather than throwing on the connect screen.
test('anything that is not a list is no rows at all', () => {
  for (const raw of [undefined, null, {}, 'servers', 7]) {
    assert.deepEqual(savedServerRows(raw), []);
  }
});

test('hasPassword is a boolean, not a truthy value', () => {
  const [row] = savedServerRows([
    { address: 'a.example:64738', username: 'gul', hasPassword: 'yes' },
  ]);
  assert.equal(row.hasPassword, false);
});

// ─── the fallback rule ───────────────────────────────────────────────────────

test('a connect that started needs no fallback', () => {
  assert.deepEqual(
    connectFallback({ reason: '', address: 'a.example:64738', username: 'gul', message: '' }),
    { kind: 'none' },
  );
});

// The case that must not be a dead end: the password is still stored, the
// keyring simply would not open, so the manual form takes over with everything
// it can prefill.
test('an unreadable password falls back to the form, prefilled', () => {
  const fallback = connectFallback({
    reason: 'password',
    address: 'a.example:64738',
    username: 'Аня',
    message: 'Не удалось прочитать сохранённый пароль.',
  });
  assert.deepEqual(fallback, {
    kind: 'form',
    address: 'a.example:64738',
    username: 'Аня',
    message: 'Не удалось прочитать сохранённый пароль.',
  });
});

test('a server that is no longer remembered refreshes the list', () => {
  const fallback = connectFallback({
    reason: 'unknown',
    address: 'gone.example:64738',
    username: '',
    message: 'Этот сервер больше не в списке.',
  });
  assert.deepEqual(fallback, { kind: 'refresh', message: 'Этот сервер больше не в списке.' });
});

// A reason this build does not know must not send the user to a password form
// for a failure that had nothing to do with a password.
test('an unknown reason refreshes rather than guessing', () => {
  const fallback = connectFallback({ reason: 'something-new', message: 'что-то пошло не так' });
  assert.deepEqual(fallback, { kind: 'refresh', message: 'что-то пошло не так' });
});

test('a failure with no message still says something', () => {
  const fallback = connectFallback({ reason: 'unknown' });
  assert.equal(fallback.kind, 'refresh');
  assert.ok(fallback.kind === 'refresh' && fallback.message.length > 0);
});

test('a result that is not an object is treated as a started connect', () => {
  for (const raw of [undefined, null, 'unknown', 7]) {
    assert.deepEqual(connectFallback(raw), { kind: 'none' });
  }
});
