import test from 'node:test';
import assert from 'node:assert/strict';
import { serverHost, serverLabel } from './serverLabel.ts';

// Every form internal/mumble/endpoint.go accepts has to reduce to the host:
// that is the one part of the address a person recognises in a 240px column.

test('the default Mumble port is noise and disappears', () => {
  assert.equal(serverHost('murmur.example.com:64738'), 'murmur.example.com');
});

test('a port that is not the default identifies the server and stays', () => {
  assert.equal(serverHost('murmur.example.com:8443'), 'murmur.example.com:8443');
});

test('a bare host is already the label', () => {
  assert.equal(serverHost('murmur.example.com'), 'murmur.example.com');
});

test('a relay URL loses its scheme and its path', () => {
  assert.equal(serverHost('wss://murmur.gulvox.com/mumble'), 'murmur.gulvox.com');
});

test('the port a relay scheme implies disappears too', () => {
  assert.equal(serverHost('wss://murmur.gulvox.com:443/mumble'), 'murmur.gulvox.com');
});

test('a relay on an unusual port keeps it', () => {
  assert.equal(serverHost('wss://murmur.gulvox.com:8443/mumble'), 'murmur.gulvox.com:8443');
});

test('an IPv4 literal behaves like a host', () => {
  assert.equal(serverHost('10.0.0.5:64738'), '10.0.0.5');
  assert.equal(serverHost('10.0.0.5:64739'), '10.0.0.5:64739');
  assert.equal(serverHost('10.0.0.5'), '10.0.0.5');
});

// The brackets are what tells a port apart from the address' own colons, so
// they are kept: "[2001:db8::1]:9000" would be unreadable without them.
test('a bracketed IPv6 literal keeps its brackets', () => {
  assert.equal(serverHost('[2001:db8::1]:64738'), '[2001:db8::1]');
  assert.equal(serverHost('[2001:db8::1]:9000'), '[2001:db8::1]:9000');
  assert.equal(serverHost('wss://[2001:db8::1]/mumble'), '[2001:db8::1]');
});

test('the colons of an unbracketed IPv6 literal are not a port', () => {
  assert.equal(serverHost('2001:db8::1'), '2001:db8::1');
  assert.equal(serverHost('::1'), '::1');
});

test('surrounding whitespace never reaches the label', () => {
  assert.equal(serverHost('  murmur.example.com:64738 '), 'murmur.example.com');
});

test('a fully qualified name drops its root dot, as the pin does', () => {
  assert.equal(serverHost('murmur.example.com.:64738'), 'murmur.example.com');
});

test('no address gives no label', () => {
  assert.equal(serverHost(''), '');
  assert.equal(serverHost('   '), '');
});

// Nothing here is a security boundary: an address that parses into nothing is
// shown as typed rather than silently blanked.
test('an address that parses into nothing is shown as it was typed', () => {
  assert.equal(serverHost('wss:///mumble'), 'wss:///mumble');
});

test('a registered server name beats the address', () => {
  assert.equal(serverLabel('murmur.example.com:64738', 'Гостиная'), 'Гостиная');
});

test('the default root name is not a name', () => {
  assert.equal(serverLabel('murmur.example.com:64738', 'Root'), 'murmur.example.com');
  assert.equal(serverLabel('murmur.example.com:64738', '   '), 'murmur.example.com');
  assert.equal(serverLabel('murmur.example.com:64738'), 'murmur.example.com');
});
