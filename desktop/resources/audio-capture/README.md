# Linux screen audio

`npm run build:audio` builds `linux-x64/gul-audio` from the source in
`native/audio-capture` using the system libpulse headers and library. The binary
is bundled only in the Linux installer. PCM stays in the local audio server;
the main process receives a readiness message and an opaque capture lease.

The helper copies eligible application playback into a private stereo source,
excluding the Gul process tree. It never changes the default output or input,
moves application streams, or records a hardware microphone. Display consent
is required before the main process starts it; helper shutdown removes its
private devices. Unknown process identities are excluded.

libpulse is dynamically linked and installed through the DEB's `libpulse0`
dependency. PulseAudio is licensed under LGPL-2.1-or-later; it is not bundled.
