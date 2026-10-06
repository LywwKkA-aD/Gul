/** Only Go may turn an authenticated remote grant into a local capability URL. */
export function realityGateway(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === 'ws:' && url.hostname === '127.0.0.1' && !!url.port &&
      Number(url.port) > 0 && !url.username && !url.password && !url.search && !url.hash &&
      /^\/[0-9a-f]{64}$/.test(url.pathname);
  } catch { return false; }
}

export function realityBrokerOrigin(url: URL): string | undefined {
  if (url.protocol !== 'livekit+vless:' || !url.hostname || url.username || url.password || url.hash ||
      (url.pathname !== '' && url.pathname !== '/')) return undefined;
  const query = url.searchParams;
  const names = ['security', 'flow', 'type', 'sni', 'pbk', 'sid'];
  if ([...query.keys()].length !== names.length || names.some((name) => query.getAll(name).length !== 1) ||
      query.get('security') !== 'reality' || query.get('flow') !== 'none' || query.get('type') !== 'tcp' ||
      !/^[A-Za-z0-9.-]+$/.test(query.get('sni') ?? '') || !/^[A-Za-z0-9_-]{43}$/.test(query.get('pbk') ?? '') ||
      !/^(?:[0-9a-f]{2}){1,8}$/.test(query.get('sid') ?? '')) return undefined;
  // The optional profile port is the outer REALITY listener. Its authenticated
  // broker always uses HTTPS 443, as validated by the Go transport.
  return new URL(`https://${url.hostname}`).origin;
}
