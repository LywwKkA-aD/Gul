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

Create the derived secret once by mounting the existing raw Murmur secret into
a short-lived relay container and piping only the derived value to Podman. Do
not put either secret on the command line:

```sh
podman run --rm \
  --secret MUMBLE_CONFIG_SERVER_PASSWORD,type=mount,target=MUMBLE_CONFIG_SERVER_PASSWORD,uid=10000,gid=10000,mode=0400 \
  localhost/gul-wss-relay@sha256:REPLACE_WITH_IMAGE_DIGEST \
  derive-credential --secret-file /run/secrets/MUMBLE_CONFIG_SERVER_PASSWORD \
  | podman secret create GUL_RELAY_BEARER -
```

The installed production Quadlet mounts only `GUL_RELAY_BEARER`. Failed
authentication is bounded per source: five failures in one minute are followed
by a five-minute temporary rejection, and at most 4096 sources are retained.
The listener also caps all pre-authentication TCP connections at 256 while no
more than 64 authenticated relay sessions may be active. Its health command is
declared in exec form because the scratch image contains no shell.

On Linux the relay derives a stable pseudonymous address inside `127/8` from
each outer source IP and binds backend connections to it. Murmur therefore
keeps separate IP-autoban buckets without learning the public source address.
Do not disable Murmur autoban. Authentication, global capacity, and per-source
IP concurrency are additionally enforced at the relay. Direct native Mumble
TCP/UDP on 64738 remains available.
