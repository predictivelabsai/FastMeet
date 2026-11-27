# Changelog: FastMeet Go (`meet/`)

All notable changes to the Go video-meet server. Versions follow semver and are
tagged `meet/vX.Y.Z`.

## 0.1.1 (2026-11-27)

- Embedded TURN: relay-to-relay calls now connect when the server runs in a
  Docker bridge network (Coolify). Packets between two of our own relay ports
  go over loopback instead of Docker's hairpin NAT, whose rewritten source
  address failed the TURN permission check.
- Production turns on the embedded TURN (UDP 3478, relay 49160-49200) so
  callers behind strict NATs, such as many mobile networks, can still connect.

## 0.1.0 (2026-11-27)

First release of the Go port, deployed at https://meet.socialagent.fun.

- Landing page with a **Create a room** button and a join-by-code box; no
  accounts, no email, no sign-up.
- Hard-to-guess room links: `/<xxxx-xxxx-xxxx>`, 60 bits of crypto randomness
  from a 32-symbol alphabet without look-alike characters.
- Real live video: peer-to-peer WebRTC in the browser (mesh, up to `MAX_PEERS`,
  default 4), replacing the Python app's embedded Jitsi room.
- Go websocket signaling (offer/answer/ICE relay with perfect negotiation),
  presence (join/leave), mic and camera state, and in-call text chat that is
  relayed and never stored.
- Lobby with camera preview, name, mic/camera toggles and a copy/share invite
  link; in-call mute, camera, chat, copy link and leave; audio-only and
  receive-only fallbacks when a camera is missing or blocked.
- Public STUN by default (Google, Cloudflare). TURN plan: short-lived TURN REST
  credentials for an external TURN (`TURN_URLS` + `TURN_SECRET`) or an embedded
  pion/turn server (`TURN_EMBEDDED=1`).
- "Powered by Social Agent" links to https://socialagent.fun on every page.
- Per-IP rate limits on room creation (6/min, 30/hour by default) and on
  websocket connects; per-connection message rate and size limits; websocket
  Origin check; strict CSP and Permissions-Policy.
- No recording and no media logging: media never reaches the server, and
  request logs carry only the method, route kind and status (no room IDs, IPs,
  names or chat).
- Distroless Docker image, `/healthz`, CI (vet, race tests, build).
