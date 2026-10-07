import { createServer, type Server } from 'node:http';
import type { Socket } from 'node:net';
import WebSocket, { WebSocketServer } from 'ws';
import { APP_ORIGIN } from '../shared/contracts.ts';

export type WindowsAudioBridgeFailure =
  'backlog' | 'invalid-consent' | 'client-message' | 'client-close' | 'socket-error';

export class WindowsAudioBridge {
  private readonly valid: () => boolean;
  private readonly onFailure: (reason: WindowsAudioBridgeFailure) => void;
  private readonly path: string;
  private readonly server: Server;
  private readonly sockets = new Set<Socket>();
  private readonly ws: WebSocketServer;
  private client?: WebSocket;
  private used = false;
  private closed = false;
  private failed = false;
  private port = 0;
  constructor(nonce: string, valid: () => boolean, onFailure: (reason: WindowsAudioBridgeFailure) => void) {
    if (!/^[a-f0-9]{48}$/u.test(nonce)) throw new Error('GUL_AUDIO_BRIDGE');
    this.path = '/' + nonce;
    this.valid = valid;
    this.onFailure = onFailure;
    this.server = createServer((_request, response) => {
      response.writeHead(404, { Connection: 'close' });
      response.end();
    });
    this.server.maxConnections = 8;
    this.server.maxHeadersCount = 20;
    this.server.headersTimeout = 3000;
    this.server.requestTimeout = 3000;
    this.ws = new WebSocketServer({
      noServer: true,
      maxPayload: 8192,
      perMessageDeflate: false,
      autoPong: false,
    });
    this.server.on('connection', (socket) => {
      this.sockets.add(socket);
      socket.setTimeout(3000, () => socket.destroy());
      socket.once('close', () => this.sockets.delete(socket));
    });
    this.server.on('upgrade', (request, socket, head) => {
      if (
        this.closed ||
        this.used ||
        !this.isValid() ||
        request.url !== this.path ||
        request.headers.origin !== APP_ORIGIN ||
        request.headers.host !== `127.0.0.1:${this.port}` ||
        request.socket.remoteAddress !== '127.0.0.1' ||
        head.length !== 0
      ) {
        socket.destroy();
        return;
      }
      this.used = true;
      request.socket.setTimeout(0);
      this.ws.handleUpgrade(request, socket, head, (client) => {
        if (!this.isValid() || this.closed) {
          client.terminate();
          return;
        }
        this.client = client;
        client.on('message', () => this.fail('client-message'));
        client.on('ping', () => this.fail('client-message'));
        client.on('pong', () => this.fail('client-message'));
        client.once('error', () => this.fail('socket-error'));
        client.once('close', () => this.fail('client-close'));
      });
    });
    this.server.on('error', () => this.fail('socket-error'));
    this.ws.on('error', () => this.fail('socket-error'));
  }
  async listen(): Promise<string> {
    await new Promise<void>((resolve, reject) => {
      const error = () => reject(new Error('GUL_AUDIO_BRIDGE'));
      this.server.once('error', error);
      this.server.listen(0, '127.0.0.1', () => {
        this.server.off('error', error);
        resolve();
      });
    });
    const address = this.server.address();
    if (this.closed || !this.isValid() || !address || typeof address === 'string') {
      await this.close();
      throw new Error('GUL_AUDIO_BRIDGE');
    }
    this.port = address.port;
    return `ws://127.0.0.1:${address.port}${this.path}`;
  }
  send(frame: Buffer): void {
    if (this.closed || this.failed || !this.isValid()) {
      this.fail('invalid-consent');
      return;
    }
    if (!this.client || this.client.readyState !== WebSocket.OPEN) return;
    if (this.client.bufferedAmount + frame.length > 46080) {
      this.fail('backlog');
      return;
    }
    this.client.send(frame, { binary: true, compress: false }, (error) => {
      if (error) this.fail('socket-error');
    });
  }
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    this.client?.terminate();
    for (const socket of this.sockets) socket.destroy();
    await Promise.all([
      new Promise<void>((resolve) => this.ws.close(() => resolve())),
      new Promise<void>((resolve) => this.server.close(() => resolve())),
    ]);
  }
  private isValid(): boolean {
    try {
      return this.valid();
    } catch {
      return false;
    }
  }
  private fail(reason: WindowsAudioBridgeFailure): void {
    if (this.closed || this.failed) return;
    this.failed = true;
    this.client?.terminate();
    this.onFailure(reason);
  }
}
