import { get } from 'node:https';

export const RELEASES_ENDPOINT = 'https://api.github.com/repos/LywwKkA-aD/Gul/releases?per_page=1';
export const RELEASE_BODY_LIMIT = 1024 * 1024;

/** Fixed HTTPS endpoint, verified TLS, no redirects, credentials or arbitrary hosts. */
export function requestReleaseList(signal: AbortSignal, client: typeof get = get): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const request = client(
      RELEASES_ENDPOINT,
      {
        signal,
        rejectUnauthorized: true,
        minVersion: 'TLSv1.2',
        headers: {
          Accept: 'application/vnd.github+json',
          'X-GitHub-Api-Version': '2022-11-28',
          'User-Agent': 'Gul-update-check',
        },
      },
      (response) => {
        if (response.statusCode !== 200) {
          response.destroy();
          reject(new Error('GUL_UPDATE_UNAVAILABLE'));
          return;
        }
        const chunks: Buffer[] = [];
        let size = 0;
        response.on('data', (chunk: Buffer) => {
          size += chunk.length;
          if (size > RELEASE_BODY_LIMIT) {
            response.destroy();
            request.destroy(new Error('GUL_UPDATE_UNAVAILABLE'));
            return;
          }
          chunks.push(chunk);
        });
        response.on('error', () => reject(new Error('GUL_UPDATE_UNAVAILABLE')));
        response.on('aborted', () => reject(new Error('GUL_UPDATE_UNAVAILABLE')));
        response.on('end', () => resolve(Buffer.concat(chunks)));
      },
    );
    request.on('error', () => reject(new Error('GUL_UPDATE_UNAVAILABLE')));
    request.setTimeout(5000, () => request.destroy(new Error('GUL_UPDATE_UNAVAILABLE')));
  });
}
