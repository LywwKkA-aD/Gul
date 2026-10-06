#!/usr/bin/env python3
"""Probe the real GTK4/WebKitGTK 6.0 runtime used by the Linux desktop client.

Run with a display, or with ``dbus-run-session -- xvfb-run -a python3
scripts/probe-linux-webrtc.py``. The test needs python3-gi, gir1.2-webkit-6.0,
xvfb, xauth and dbus-x11 on Ubuntu. It uses only a local synthetic document
and two local peer connections; it never reads Gul credentials or uses a server.

Exit codes: 0 = local data channel works, 2 = RTC API is unavailable,
1 = an advertised RTC API failed. --capabilities-only also accepts code 2.

Ubuntu 24.04 amd64 and arm64 with libwebkitgtk-6.0-4 2.52.6 reproduce
``peerConnection: undefined`` even after enabling the runtime setting and adding
gstreamer1.0-plugins-bad/gstreamer1.0-nice. Installing those plugins alone cannot
restore an API absent from the distro's WebKit build. WebKit's matching source
makes ENABLE_WEB_RTC depend on experimental features; Ubuntu's build rules do
not opt into that feature. Keep this a capability probe, not a package-version
allowlist: a future distributor build may enable the API.

Primary sources:
https://github.com/WebKit/WebKit/blob/webkitgtk-2.52.6/Source/cmake/OptionsGTK.cmake
https://git.launchpad.net/ubuntu/+source/webkit2gtk/tree/debian/rules?h=ubuntu/noble-security
"""

import argparse
import json
import sys

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("WebKit", "6.0")
from gi.repository import Gio, GLib, Gtk, WebKit  # noqa: E402

DOCUMENT = r"""<!doctype html><meta charset="utf-8"><script>
(async () => {
  const result = {
    secureContext: isSecureContext,
    peerConnection: typeof RTCPeerConnection,
    webkitPeerConnection: typeof webkitRTCPeerConnection,
    mozPeerConnection: typeof mozRTCPeerConnection,
    mediaDevices: typeof navigator.mediaDevices,
    displayCapture: typeof navigator.mediaDevices?.getDisplayMedia,
  };
  const report = () => window.webkit.messageHandlers.probe.postMessage(JSON.stringify(result));
  const PeerConnection = globalThis.RTCPeerConnection || globalThis.webkitRTCPeerConnection || globalThis.mozRTCPeerConnection;
  if (typeof PeerConnection !== 'function') {
    result.status = 'unavailable';
    report();
    return;
  }
  let sender;
  let receiver;
  let deadline;
  try {
    sender = new PeerConnection();
    receiver = new PeerConnection();
    result.audioCodecs = RTCRtpReceiver.getCapabilities('audio').codecs.map(c => c.mimeType);
    result.videoCodecs = RTCRtpReceiver.getCapabilities('video').codecs.map(c => c.mimeType);
    const pendingForSender = [];
    const pendingForReceiver = [];
    sender.onicecandidate = event => {
      if (!event.candidate) return;
      if (receiver.remoteDescription) receiver.addIceCandidate(event.candidate).catch(() => {});
      else pendingForReceiver.push(event.candidate);
    };
    receiver.onicecandidate = event => {
      if (!event.candidate) return;
      if (sender.remoteDescription) sender.addIceCandidate(event.candidate).catch(() => {});
      else pendingForSender.push(event.candidate);
    };
    const received = new Promise((resolve, reject) => {
      deadline = setTimeout(() => reject(new Error('Local data channel timed out')), 10000);
      receiver.ondatachannel = event => {
        event.channel.onmessage = message => {
          if (message.data === 'gul-runtime-probe') resolve();
        };
      };
    });
    // Handle a timeout even if negotiation fails before we await the channel.
    received.catch(() => {});
    const channel = sender.createDataChannel('gul-runtime-probe');
    channel.onopen = () => channel.send('gul-runtime-probe');
    sender.addTransceiver('audio', {direction: 'recvonly'});
    sender.addTransceiver('video', {direction: 'recvonly'});
    const offer = await sender.createOffer();
    await sender.setLocalDescription(offer);
    await receiver.setRemoteDescription(offer);
    await Promise.all(pendingForReceiver.map(candidate => receiver.addIceCandidate(candidate)));
    const answer = await receiver.createAnswer();
    await receiver.setLocalDescription(answer);
    await sender.setRemoteDescription(answer);
    await Promise.all(pendingForSender.map(candidate => sender.addIceCandidate(candidate)));
    await received;
    result.status = 'available';
  } catch (error) {
    result.status = 'failed';
    result.errorName = error.name;
    result.errorMessage = error.message;
  } finally {
    clearTimeout(deadline);
    sender?.close();
    receiver?.close();
    report();
  }
})();
</script>"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--capabilities-only", action="store_true")
    args = parser.parse_args()
    Gtk.init()
    loop = GLib.MainLoop()
    exit_status = 1
    metadata = {
        "webkitVersion": ".".join(str(part) for part in (
            WebKit.get_major_version(),
            WebKit.get_minor_version(),
            WebKit.get_micro_version(),
        )),
    }
    manager = WebKit.UserContentManager()
    manager.register_script_message_handler("probe", None)

    def report(_manager, value):
        nonlocal exit_status
        result = {**metadata, **json.loads(value.to_string())}
        print(json.dumps(result, sort_keys=True), flush=True)
        if result["status"] == "available":
            exit_status = 0
        elif result["status"] == "unavailable":
            exit_status = 0 if args.capabilities_only else 2
        loop.quit()

    manager.connect("script-message-received::probe", report)
    view = WebKit.WebView(user_content_manager=manager)
    settings = view.get_settings()
    metadata["defaultWebRTCSetting"] = settings.get_enable_webrtc()
    # A successful getter after this call does not prove WebRTC was compiled in.
    settings.set_enable_webrtc(True)
    metadata["requestedWebRTCSetting"] = settings.get_enable_webrtc()
    settings.set_hardware_acceleration_policy(WebKit.HardwareAccelerationPolicy.NEVER)
    body = DOCUMENT.encode()

    def serve_document(request):
        stream = Gio.MemoryInputStream.new_from_bytes(GLib.Bytes.new(body))
        request.finish(stream, len(body), "text/html")

    # Match Wails' built-in document origin, without disabling web security.
    view.get_context().register_uri_scheme("wails", serve_document)
    window = Gtk.Window()
    window.set_child(view)
    window.set_default_size(400, 300)
    window.present()
    view.load_uri("wails://wails/")

    def timeout():
        print(json.dumps({**metadata, "status": "timeout"}), flush=True)
        loop.quit()
        return False

    GLib.timeout_add_seconds(20, timeout)
    loop.run()
    return exit_status


if __name__ == "__main__":
    sys.exit(main())
