import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import type { ClientRequest, IncomingMessage } from 'node:http';
import type { get, RequestOptions } from 'node:https';
import test from 'node:test';
import { RELEASES_ENDPOINT, RELEASE_BODY_LIMIT, requestReleaseList } from '../src/main/updates-network.ts';

function fixture(
  status = 200,
  chunks: readonly Buffer[] = [Buffer.from('[]')],
  failure?: 'timeout' | 'network' | 'body' | 'aborted',
) {
  const response = new EventEmitter() as IncomingMessage;
  response.statusCode = status;
  let destroyed = false;
  response.destroy = () => {
    destroyed = true;
    return response;
  };
  const request = new EventEmitter() as ClientRequest;
  request.destroy = (error?: Error) => {
    request.emit('error', error);
    return request;
  };
  let timeout!: () => void;
  let timeoutValue = 0;
  request.setTimeout = (value, listener) => {
    timeoutValue = value;
    timeout = listener!;
    return request;
  };
  let capturedURL: unknown;
  let capturedOptions!: RequestOptions;
  const client = ((url: unknown, options: RequestOptions, callback: (value: IncomingMessage) => void) => {
    capturedURL = url;
    capturedOptions = options;
    queueMicrotask(() => {
      if (failure === 'timeout') {
        timeout();
        return;
      }
      if (failure === 'network') {
        request.emit('error', Error('private network error'));
        return;
      }
      callback(response);
      if (status !== 200) return;
      if (failure === 'body') {
        response.emit('error', Error('private body error'));
        return;
      }
      if (failure === 'aborted') {
        response.emit('aborted');
        return;
      }
      chunks.forEach((chunk) => response.emit('data', chunk));
      if (!destroyed) response.emit('end');
    });
    return request;
  }) as typeof get;
  return { client, inspect: () => ({ capturedURL, capturedOptions, timeoutValue, destroyed }) };
}

test('update requests use only the pinned HTTPS endpoint with TLS verification and bounded deadline', async () => {
  const { client, inspect } = fixture(200, [Buffer.from('['), Buffer.from(']')]);
  const signal = new AbortController().signal;
  assert.equal(Buffer.from(await requestReleaseList(signal, client)).toString(), '[]');
  const { capturedURL, capturedOptions, timeoutValue } = inspect();
  assert.equal(capturedURL, RELEASES_ENDPOINT);
  assert.equal(capturedOptions.signal, signal);
  assert.equal(capturedOptions.rejectUnauthorized, true);
  assert.equal(capturedOptions.minVersion, 'TLSv1.2');
  assert.equal(timeoutValue, 5000);
  assert.deepEqual(capturedOptions.headers, {
    Accept: 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28',
    'User-Agent': 'Gul-update-check',
  });
});

test('redirects, rate limits, oversized bodies and aborted/timed-out sockets expose only a fixed error', async () => {
  for (const scenario of [
    fixture(302),
    fixture(429),
    fixture(200, [Buffer.alloc(RELEASE_BODY_LIMIT + 1)]),
    fixture(200, [], 'timeout'),
    fixture(200, [], 'network'),
    fixture(200, [], 'body'),
    fixture(200, [], 'aborted'),
  ])
    await assert.rejects(
      requestReleaseList(new AbortController().signal, scenario.client),
      /^Error: GUL_UPDATE_UNAVAILABLE$/u,
    );
});
