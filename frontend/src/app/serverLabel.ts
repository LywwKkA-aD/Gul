// What the sidebar calls the server we are on.
//
// The address the user typed is a dial string, not a name: it can be a bare
// host, a host:port, or a relay URL with a scheme and a path
// (internal/mumble/endpoint.go accepts all three). The identity inside all of
// them is the host, so that is what the header shows - the full address stays
// one hover away.
//
// Kept free of React and of the DOM so the rules are testable (serverLabel.test.ts).

/** Mumble's default port. An address that carries only this says nothing the
    label has to show (gumble.DefaultPort). */
export const MUMBLE_DEFAULT_PORT = '64738';

/** Ports the scheme already implies, so printing them adds nothing. */
const SCHEME_DEFAULT_PORT: Record<string, string> = {
  wss: '443',
  https: '443',
  ws: '80',
  http: '80',
};

/** The root channel of a default Mumble install. Not a name, so it never wins
    over the address. */
const DEFAULT_ROOT_NAME = 'Root';

/** Splits an authority into host and port, leaving a bracketed IPv6 literal
    whole. A bare IPv6 literal has no port: its colons are part of the address. */
function splitHostPort(authority: string): { host: string; port: string } {
  if (authority.startsWith('[')) {
    const end = authority.indexOf(']');
    if (end < 0) return { host: authority, port: '' };
    const rest = authority.slice(end + 1);
    return { host: authority.slice(0, end + 1), port: rest.startsWith(':') ? rest.slice(1) : '' };
  }
  const first = authority.indexOf(':');
  if (first < 0) return { host: authority, port: '' };
  if (authority.indexOf(':', first + 1) >= 0) return { host: authority, port: '' };
  return { host: authority.slice(0, first), port: authority.slice(first + 1) };
}

/** Drops the trailing root dot of a fully qualified name, the way the Go side
    canonicalizes it before pinning the certificate. */
function trimRootDot(host: string): string {
  return host.length > 1 && host.endsWith('.') ? host.slice(0, -1) : host;
}

/**
 * The host of an accepted server address, plus a port only when it carries
 * information: `host:64738` and `wss://host:443/mumble` are the default ways
 * to reach a server, `host:8443` is not.
 *
 * Anything unparseable is returned trimmed rather than swallowed: showing the
 * raw string beats showing nothing.
 */
export function serverHost(address: string): string {
  const value = address.trim();
  if (value === '') return '';

  const scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):\/\//.exec(value);
  const rest = scheme ? value.slice(scheme[0].length) : value;

  // Everything from the first path, query or fragment separator is routing,
  // not identity - `wss://host/mumble` names the same server as `host`.
  let authority = rest.split(/[/?#]/)[0];
  const at = authority.lastIndexOf('@');
  if (at >= 0) authority = authority.slice(at + 1);
  if (authority === '') return value;

  const { host, port } = splitHostPort(authority);
  if (host === '') return value;

  const implied = scheme ? SCHEME_DEFAULT_PORT[scheme[1].toLowerCase()] : undefined;
  const noise = port === '' || port === MUMBLE_DEFAULT_PORT || port === implied;
  return noise ? trimRootDot(host) : `${trimRootDot(host)}:${port}`;
}

/**
 * What to print as the server's name.
 *
 * A registered server carries its name in the root channel, and that beats any
 * address; a default install leaves the literal "Root" behind, which is not a
 * name and falls through to the host.
 */
export function serverLabel(address: string, rootName?: string): string {
  const name = rootName?.trim() ?? '';
  if (name !== '' && name !== DEFAULT_ROOT_NAME) return name;
  return serverHost(address);
}
