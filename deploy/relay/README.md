# Gul WSS relay

The relay exposes one protocol endpoint:

```text
wss://murmur.gulvox.com/mumble -> 127.0.0.1:64738
```

The Gul client derives a domain-separated bearer credential from the server
join password. The relay stores only that derived credential in the separate
`GUL_RELAY_BEARER` Podman secret; the raw Mumble password is never mounted into
the production relay. It requires the `gul-mumble-v1` WebSocket subprotocol,
accepts binary frames only, and carries the inner Mumble TLS stream without
inspecting it. The destination is compiled into the binary; request data cannot
select another target.

Production runs as the same rootless Podman user as Murmur and joins only
Murmur's existing pasta network namespace. Murmur's owner Quadlet publishes
host port 8443 for the relay listener; the relay can reach Murmur at loopback
without gaining access to unrelated host-local services. It reads only the
`acme` subdirectory of Murmur's data volume. Firewalld redirects public TCP 443
to 8443; 8443 itself must not be opened in the public zone.

Build the Linux amd64 binary with `CGO_ENABLED=0`, collect the legal bundle,
then build the scratch image from this directory. Replace the image digest in
`gul-relay.container.example` before installing it as
`~/.config/containers/systemd/gul-relay.container`.

## Bearer credentials

Create the derived secret once by mounting the existing raw Murmur secret into
a short-lived relay container and piping only the derived values to Podman. Do
not put either secret on the command line:

```sh
podman run --rm \
  --secret MUMBLE_CONFIG_SERVER_PASSWORD,type=mount,target=MUMBLE_CONFIG_SERVER_PASSWORD,uid=10000,gid=10000,mode=0400 \
  localhost/gul-wss-relay@sha256:REPLACE_WITH_IMAGE_DIGEST \
  derive-credential --secret-file /run/secrets/MUMBLE_CONFIG_SERVER_PASSWORD \
  | podman secret create GUL_RELAY_BEARER -
```

`derive-credential` writes one credential per line: the current PBKDF2
credential (`v2.` prefix) first, then the single-HMAC credential that clients up
to v0.3.0-alpha.2 send. The relay derives nothing at startup and nothing per
request; it compares the presented header against these precomputed values.

A file without a `v2.` line is refused at startup: an existing deployment that
still holds only the alpha.2 credential must recreate the secret with the
command above before this version starts.

Legacy lifecycle:

1. This release runs with `--accept-legacy-bearer` (the default). Every request
   that matched the legacy credential is logged at warn level with its source,
   so the journal shows exactly who still has to update.
2. Once no such line has appeared for a full release cycle, recreate the secret
   with `derive-credential --v2-only` and add `--accept-legacy-bearer=false` to
   the `Exec=` line. Either change alone is enough to stop accepting it; doing
   both removes the value from the host as well.

## Host firewall

The relay caps accepted TCP connections at 16 per source (IPv4 keyed by /32,
IPv6 by /64) and 256 in total, and it keeps accepting at either cap instead of
suspending its accept loop. The per-source figure is twice the 8 sessions one
source may run, so a source at its session quota still has room to open a
handshake.

That cap is the last line, not the first. Add a connection limit on the public
port as well, so the kernel drops a flood before it costs a TLS handshake. The
rules match the pre-redirect port 443 and sit well above the relay's own
per-source limit, so ordinary use never reaches them:

```sh
# nftables
nft add rule inet filter input tcp dport 443 ct state new \
  meter relay4 { ip saddr ct count over 32 } reject with tcp reset
nft add rule inet filter input tcp dport 443 ct state new \
  meter relay6 { ip6 saddr and ffff:ffff:ffff:ffff:: ct count over 32 } reject with tcp reset
```

```sh
# firewalld (direct rules, iptables backend)
firewall-cmd --permanent --direct --add-rule ipv4 filter INPUT_direct 0 \
  -p tcp --dport 443 -m connlimit --connlimit-above 32 -j REJECT --reject-with tcp-reset
firewall-cmd --permanent --direct --add-rule ipv6 filter INPUT_direct 0 \
  -p tcp --dport 443 -m connlimit --connlimit-above 32 --connlimit-mask 64 -j REJECT --reject-with tcp-reset
firewall-cmd --reload
```

## Limits

Failed authentication is bounded per source: five failures in one minute are
followed by a one-minute temporary rejection, and at most 4096 sources are
retained. The ban keys on the source address, which behind a NAT is shared by
everyone on it, so it is deliberately short and every rejection carries
`Retry-After`; a client that honors it waits rather than treating the rejection
as final. At most 64 authenticated relay sessions may be active, 8 per source.

A session that transfers nothing in either direction for 60 s is closed, and a
single write that stays blocked for 30 s ends the session. Mumble pings every
few seconds, so only a wedged peer trips either bound. On shutdown the relay
stops accepting, sends every session a close frame, waits up to 5 s for them to
end, and cuts off whatever remains, all inside a 10 s budget.

## Logging

The daemon logs JSON to stderr, which journald collects. `--log-level` selects
the level (`info` by default). What an operator sees per failure class:

| Event | Level | Key fields |
| --- | --- | --- |
| Session opened and closed | info | `source`, `duration`, `bytes_from_client`, `bytes_to_client`, `reason` |
| Rejected credential (401) | warn | `source`, `credential` (`missing`, `malformed`, `legacy`, `v2`) |
| Legacy credential accepted | warn | `source` |
| Ban activated (429) | warn | `source`, `retry_after` |
| Relay full (503) | warn | `source`, `scope` (`global`, `source` or `shutdown`) |
| Pre-authentication connections rejected | warn | `scope`, `rejected` since the last line |
| Upstream dial failure | error | `source`, `upstream`, `error` |
| Certificate reload failure | error | `cert`, `key`, `error` |
| Rate-limited rejections, TLS handshake noise | debug | as above |

Neither the Authorization header nor any credential is ever logged: rejections
record the shape of what was presented, not its value. Repeated rejections are
throttled on purpose, because their rate is chosen by whoever is knocking: only
the request that activates a ban is logged at warn, the ones that follow it are
debug, and the pre-authentication limiter reports a count at most every 30 s.

A certificate pair that stops loading keeps the last valid pair in service
rather than dropping sessions. That failure is logged at most every five
minutes and reported by `/healthz`, which stays 200 and adds a
`certificate-reload: failing` line; the health command prints a notice for it
on stderr. It must not fail the check, because `HealthOnFailure=kill` plus
`StartLimitBurst` would turn a bad certificate file into a unit that refuses to
start. The health command verifies the served certificate against the deployed
leaf chain first and falls back to the host trust store, because the loader
picks up an ACME renewal up to 30 s after the file changes.

`/healthz` is served only to loopback sources. That is sufficient because the
relay shares Murmur's rootless pasta namespace, where public connections arrive
from the namespace gateway address and only the container's own health command
reaches the endpoint over loopback. A deployment that does not preserve source
addresses that way must block `/healthz` at the firewall instead. The health
command is declared in exec form because the scratch image contains no shell.

## Source pseudonymization

On Linux the relay derives a stable pseudonymous address inside `127/8` from
each outer source IP and binds backend connections to it. Murmur therefore
keeps separate IP-autoban buckets without learning the public source address.
The network address `127.0.0.0`, the broadcast address `127.255.255.255` and
Murmur's own `127.0.0.1` are remapped, because Linux refuses to bind the first
two and a source that hashed onto one of them would be locked out silently.

This is Linux-only: other systems assign just `127.0.0.1`, so every session
there shares one identity and one autoban bucket. Only the production Linux
deployment gets per-source buckets.

Do not disable Murmur autoban. Authentication, global capacity, and per-source
concurrency are additionally enforced at the relay. Direct native Mumble
TCP/UDP on 64738 remains available.
