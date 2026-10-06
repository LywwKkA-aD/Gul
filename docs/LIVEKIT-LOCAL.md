# Local LiveKit client and screen lab

This lab runs only on the developer's computer. It does not configure a VPS,
change the deployed LiveKit server, or use its REALITY connection. It is not a deployment
configuration: the token endpoint trusts local processes and grants access to
one local room without user authentication.

## Complete client

The normal client entry point now uses LiveKit for voice, channels and chat.
Its existing native audio engine handles microphone processing, PTT, devices,
individual volumes/mutes, and playback including screen audio. The webview handles
screen capture/publication and remote video where supported; otherwise the
compact screen button opens the browser companion. Neither opens a microphone.

Start the local server as below, then on macOS:

```sh
bash scripts/livekit-client.sh
```

This produces `bin/livekit-local/Gul LiveKit.app`. Use address
`http://127.0.0.1:8787`, any nickname, and leave the password empty. Channels are
Общая, Игра, AFK and the root. Double-click a channel to join it. The compact screen icon is in the bottom bar beside microphone/deafen. Remote
video appears inside the chat; the idle screen panel takes no space. Capture
starts only after clicking the icon and completing the OS source picker; changing channel, reconnecting or disconnecting stops it.

The separate bundle identifier is `io.github.lywwkkaad.gul.livekit`. Settings
are kept in the OS configuration directory under `gul-livekit`; the existing
Gul settings and credential-store namespace are not reused. For independent
instances, launch the executable with distinct absolute `GUL_CONFIG_DIR` values.
`--build-only` builds without launching. This preview does not perform automatic
GitHub release checks. Native notifications are opt-in with
`GUL_LOCAL_NOTIFICATIONS=1`.

Voice remains 48 kHz mono through the existing DSP. Received stereo screen
audio is currently downmixed to mono; per-user gain/mute and global deafen also
apply to it. Capturing the whole system can include received voices and create
feedback: source/application audio isolation still needs platform testing.
No reconnect automatically resumes screen capture. Chat history is in memory,
and only currently joined participants receive a message.

The desktop also accepts remote HTTPS and `livekit+vless://` REALITY profiles.
Mumble and Hysteria remain as legacy source and regression tests, outside the
current GUI runtime dependency graph. The shared REALITY transport is active
when the new LiveKit profile is selected. The isolated server below remains
local-only.

On Ubuntu 24.04, stock WebKitGTK has no WebRTC constructors even when its runtime
setting is enabled. Installing additional GStreamer plugins did not change
that result. `scripts/probe-linux-webrtc.py` reproduces the capability check.
The browser companion uses a one-use loopback handoff, keeps grants in memory,
and stops on native channel changes, disconnects, or lost control connection.
Voice and screen-audio playback remain in the native client.

## Run

Start Docker (OrbStack on macOS), then from the repository root:

The server bootstrap currently supports macOS/Linux. Native Windows config
initialization is rejected because this prototype protects credentials with
POSIX file modes, not Windows ACLs. This restriction does not apply to the
Windows GUI client. Running the server through WSL2 has not been verified.

```sh
npm --prefix frontend ci
GOTOOLCHAIN=go1.26.7 wails3 generate bindings -ts -i
npm --prefix frontend run build
bash scripts/livekit-local.sh up
```

Open `http://127.0.0.1:8787/#livekit` in two browser tabs, using different
participant identities. The room is fixed to `gul-local`. Screen capture needs
an explicit user action and the browser/OS sharing prompt. Audio availability
depends on the browser and selected capture source; absence of a captured audio
track must be shown in the UI.

### Native Gul window on macOS

With the local server running:

```sh
bash scripts/livekit-native.sh
```

This builds and launches `bin/livekit-local/Gul LiveKit Lab.app`, with a separate
bundle identifier. Its entry point skips core, settings, hotkeys, Mumble and the
native voice engine. The compiled lab flag also keeps Finder launches isolated.
`--build-only` builds without opening the window. A normal Gul build can opt into
the same isolated mode with `GUL_LIVEKIT_LAB=1`; without it the full LiveKit client starts.
The lab remains an isolated screen experiment alongside the full chat UI.

On this Mac, the embedded WKWebView successfully captured the desktop but returned
no system-audio track. Chrome tab capture with audio was verified separately. Do
not interpret a working desktop video track as working game/system audio. Whole
system capture can include Gul's native voice playback and cause voice feedback;
`restrictOwnAudio` excludes the capturing web page, not the native miniaudio output.

```sh
bash scripts/livekit-local.sh status
bash scripts/livekit-local.sh down
```

`down` stops only this lab's broker and labelled `gul-livekit-local` container.
Credentials remain in ignored `bin/livekit-local/` for subsequent tests. `up`
reuses a running broker; after backend/config changes, run `down` then `up`.
Rebuilding `frontend/dist` updates the browser lab after reloading its page.
For task runners that terminate background child processes, use
`bash scripts/livekit-local.sh up --foreground` and keep that terminal open.

## Components and limits

- LiveKit server: `v1.13.8`, Docker image pinned by digest in the startup script.
- Signalling: `127.0.0.1:7880/TCP`; ICE: `127.0.0.1:7881/TCP` and
  `127.0.0.1:7882/UDP`. The container advertises `127.0.0.1` to host browsers.
  Container loopback candidate gathering stays disabled: enabling it duplicates
  the mapped UDP candidate and can prevent ICE from connecting through Docker.
  The explicit STUN list contains only `127.0.0.1:7882`, suppressing LiveKit's
  public STUN defaults. Local connections use host ICE candidates; the ICE mux
  is not a standalone STUN service. Browser clients override the list to empty.
- Token broker and static frontend: `127.0.0.1:8787`, Go standard library only.
- Private API key/secret and matching server YAML: generated cryptographically,
  stored with mode `0600`, never returned to the frontend or passed in arguments.
- Participant tokens: HS256, five-minute expiry, `gul-local` join/subscribe and
  publish grants limited to `screen_share` and `screen_share_audio`. No microphone,
  camera, data publishing, room administration or API grants.
- Broker requests require loopback peer and exact loopback Host. Browser origins
  are restricted to `http://127.0.0.1:8787`, `http://localhost:8787`, and
  `http://127.0.0.1:9245` (Vite development). JSON POST only, bounded request size,
  no wildcard CORS, no token logging, and no caching.
- The lab has no TURN service. Passing local tests does not establish that remote
  users behind ISP filtering will connect; that needs a separate transport test.

## Local API

`GET /healthz` checks broker liveness. `POST /api/livekit/token` takes:

```json
{"identity":"tester-1","room":"gul-local"}
```

It returns `url`, `token`, `identity`, and `room`. Omitting `room` selects
`gul-local`; other rooms are rejected. Identities are limited to 64 UTF-8 bytes
of letters, digits, hyphen, underscore, and full stop. Use different identities
for simultaneous clients: a duplicate identity replaces its previous session.

```sh
GOTOOLCHAIN=go1.26.7 go test -race ./internal/livekitlab ./cmd/livekit-lab -cover
```

Protocol/configuration references:
[local LiveKit setup](https://docs.livekit.io/transport/self-hosting/local/),
[v1.13.8 server configuration](https://github.com/livekit/livekit/blob/v1.13.8/config-sample.yaml),
[screen sharing](https://docs.livekit.io/transport/media/screenshare/).

## Verification

The deterministic end-to-end test publishes a canvas and a generated tone between
two real browser clients. It verifies decoded video frames, nonzero received PCM,
track removal on stop/leave and successful rejoining. It does not exercise OS
capture permission prompts. Existing unrelated room participants may stay present.

```sh
npm --prefix frontend test
npm --prefix frontend run lint
npm --prefix frontend run test:livekit
```

On macOS the test uses installed Google Chrome. On other hosts install Playwright's
Chromium or set `GUL_TEST_BROWSER` to the browser executable. No capture-permission
bypass flags are used. Traces/videos/screenshots are disabled because they could
contain local room credentials or screen contents.

For the real browser capture test:

1. Open `http://127.0.0.1:8787/livekit-source.html` in Chrome and enable its quiet tone.
2. In a separate lab tab, join as `chrome-capture`, click screen sharing, select
   the source tab and enable tab audio in Chrome's picker.
3. Run `GUL_LIVEKIT_CAPTURE_TEST=1 npm --prefix frontend run test:livekit -- capture.spec.ts`.
4. Stop sharing and stop the source's tone when finished.

Verified locally on 2026-10-06: both E2E scenarios passed; real captured audio
peak 0.0358 at the receiver with decoded moving video. The native Gul window also
displayed that incoming capture. Frontend unit tests: 118 pass. Go race tests pass;
new broker coverage 89.8%, native bridge 94.4%; controller line coverage 93.98%,
pattern 100%. Windows WebView2/system-game audio and remote ISP connectivity
remain unverified. No VPS deployment or release was performed.

## Full-client local broker

The same loopback HTTP process also exposes `/api/gul/*` for the full-client
LiveKit experiment. The original `/api/livekit/token` endpoint and `gul-local`
screen lab remain separate and unchanged. New routes become available after
rebuilding and restarting the local broker.

This is a trusted-local-process experiment, not remote user authentication.
`POST /api/gul/login` accepts a nickname and an **empty** password; a nonempty
password is rejected rather than silently ignored. Nicknames are trimmed and
limited to 64 Unicode characters, without control characters. Equal display
names are allowed; cryptographically random session identities distinguish users.

| Method and path | Request | Response |
| --- | --- | --- |
| `POST /api/gul/login` | `username`, `password` | Logical session, opaque `sessionToken`, initial voice `grant` |
| `GET /api/gul/state` | None | Global channel tree, `selfSession`, `selfChannel`, `revision` |
| `POST /api/gul/channel` | `channelId` | Same session with a voice grant for the selected channel |
| `POST /api/gul/audio` | `muted`, `deafened` | Normalized flags; deafen implies mute |
| `POST /api/gul/screen` | `channelId`, `revision` | Screen companion grant for that exact current generation |
| `POST /api/gul/logout` | No body, or `{}` | `204`; removes the logical session |

Every endpoint except login requires `Authorization: Bearer <sessionToken>`.
The opaque token stays in native Go, never in the webview. Only its SHA-256 hash
is stored by the broker. Session leases last 60 seconds; a successful state read
renews the lease. Expired sessions are pruned on subsequent session operations.
There are at most 128 logical broker sessions. Request bodies are limited to
4096 bytes, must be UTF-8 JSON objects, and reject unknown fields.

The fixed channel tree is root `0` (Gul LiveKit), `1` (Общая), `2` (Игра), and
`3` (AFK). All four are joinable; login starts in channel `1`. A channel maps to
LiveKit room `gul-channel-<channelId>`. Empty channels remain in the tree.
`revision` starts at `1` and increments only when that caller changes channels.
Joining the same channel is idempotent; another user's changes and audio flags
do not invalidate a screen grant. A mismatching screen channel or revision
returns `409`. The client must stop the old capture/room before using a new grant.

Each logical session has native identity `voice.<sessionId>` and companion
identity `screen.<sessionId>`. The roster contains only one logical user, with
key `s:livekit:<sessionId>`. Native grants may publish microphone and reliable
data; companion grants may publish screen video/audio and no data. Both may
subscribe. Tokens include server-authored owner identity, role, session, channel,
and generation attributes; clients cannot change their own metadata. Grants are
room-scoped, expire initially after five minutes, and provide no administrative
permissions. The SFU limit counts media participants: its 16 room slots support
eight logical users with both voice and screen companion connections.

This broker tracks the local client's requested membership, not confirmed SFU
presence. Chat travels through LiveKit reliable data in the native client; the
broker does not store or replay chat. Its state and sessions are in memory.
Logout/lease expiry prevents new grants but does not revoke existing self-hosted
LiveKit JWTs or disconnect existing media connections. Native Go must explicitly
close its voice and screen connections. Production authentication, authoritative
SFU reconciliation and revocation remain separate work before remote deployment.

```sh
GOTOOLCHAIN=go1.26.7 go test -race ./internal/livekitlab ./internal/livekitapi -cover
```


## Full-client verification (2026-10-06)

- All Go packages passed the default race suite. Native LiveKit unit + real SFU
  tests passed together at 89.3% statement coverage; native audio is 82.1%, core
  95.3%, local broker 94.5%. Reordering/loss tests compare real Opus/PLC PCM.
- The native macOS app joined with the real Go services. A separate local Go
  participant received and decoded nonzero microphone PCM; chat arrived in both
  directions. Muting stopped voiced transmission. Channel switching preserved
  mute/deafen and replaced the screen companion successfully.
- The screen Panel passed three browser integration tests using the real SDK/SFU.
  Two additional production-built MainScreen tests cover delayed lazy loading,
  repeated status/tree updates, real video between clients and grant retry. The
  Wails service bridge is substituted in these browser tests. Frontend unit tests: 130.
  Real tab+audio capture was verified in the earlier screen lab. In the complete
  native app the OS picker was opened then canceled; a new native system-audio
  capture was not verified in that pass.
- Native device opening is intermittent on this Mac: three hardware smoke tests
  and one full app run succeeded, but a later launch waited six minutes before
  returning an error. An independent device test also hung while that happened.
  A native stack sample located the wait in CoreAudio's CurrentDevice property
  assignment, before DSP starts. The underlying cause remains unconfirmed; OS
  permissions and audio services were not changed. Lifecycle shutdown waits at
  most 250 ms for a native call; it cannot forcibly cancel OS device opening.
- The compact bottom-bar screen icon was checked in the native build. One screen
  connection stalled, then reported disconnection; retry connected immediately.
  The icon opened the OS picker and its second click cleared pending capture.
  The test app was quit afterward. This does not establish reliable native
  startup or native system-audio capture; both still need further testing.
- Full-client voice/channel lifecycle tests cover blocked starts/stops, latest
  device selection, stale device-loss callbacks, disconnect and bounded shutdown.
- The runtime vulnerability scan reported no reachable vulnerable symbols;
  dependency-only advisories remain informational. Native macOS build/signature,
  vet/lint and dependency resolution for Windows/Linux/macOS passed. This is not
  a Windows binary or a Windows screen/game-audio test.

```sh
GUL_LIVEKIT_LIVE=1 GOTOOLCHAIN=go1.26.7 go test -race -tags live ./internal/livekit -count=1 -timeout=2m
cd frontend && npx playwright test --config playwright.panel.config.ts
npx playwright test --config playwright.mainscreen.config.ts
```

For a temporary native UI peer without a microphone or saved PCM:

```sh
GUL_LIVEKIT_PEER=1 GUL_LIVEKIT_PEER_DURATION=180s GOTOOLCHAIN=go1.26.7 go test -tags live ./internal/livekit -run '^TestLocalSFUInteractivePeer$' -v -count=1 -timeout=4m
```

It joins as `Local test peer`, sends periodic test chat and reports aggregate
receive counters. Add `GUL_LIVEKIT_PEER_TONE=1` for a generated tone. It logs out
at the end. The full client currently leaves RTT blank: the native SDK adapter
has not yet exposed a media RTT sample; no unrelated HTTP latency is substituted.
