#!/bin/sh
set -eu
# Do not change the machine's global policy or unrelated services.
# Docker uses host networking here, so INPUT is the relevant chain.
for tool in iptables ip6tables; do
    # Commit the replacement chain atomically, including on a later restart.
    "$tool-restore" -w --noflush <<'RULES'
*filter
:GUL_LIVEKIT_INPUT - [0:0]
-F GUL_LIVEKIT_INPUT
-A GUL_LIVEKIT_INPUT -i lo -j RETURN
-A GUL_LIVEKIT_INPUT -p tcp -m multiport --dports 5349,7880,8080,8787 -j REJECT
-A GUL_LIVEKIT_INPUT -j RETURN
COMMIT
RULES
    "$tool" -w -C INPUT -j GUL_LIVEKIT_INPUT 2>/dev/null || "$tool" -w -I INPUT 1 -j GUL_LIVEKIT_INPUT
done
