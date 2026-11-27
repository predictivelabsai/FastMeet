# FastMeet Go: instant video calls

A Go port of FastMeet's room, focused on what the Python app could not do
itself: **live video**. One click makes a private call link; the other person
opens it, allows the camera and talks. No email, no account, no app.

Live at **https://meet.socialagent.fun**. Powered by [Social Agent](https://socialagent.fun).

## How it works

- `GET /` landing page; `POST /new` (rate-limited) redirects to a fresh room
  `/<xxxx-xxxx-xxxx>`; `GET /join?code=` accepts a code or a full link.
- `GET /<room>` serves the room page. Rooms are stateless: any well-formed ID is
  a room, so links survive restarts, and a room exists in memory only while
  someone is in it.
- `GET /ws/<room>` is the signaling websocket. The browser does peer-to-peer
  WebRTC; the server only relays SDP/ICE, presence, mic/camera state and chat.
  Media never touches the server and nothing is recorded or stored.
- `GET /healthz` returns `{"ok":true,"version":"…"}`.

## Run

```bash
cd meet
go run .                       # http://localhost:8080
go test -race ./...
docker build -t fastmeet-go . && docker run -p 8080:8080 fastmeet-go
```

## Configuration (environment)

| Variable | Default | Meaning |
|---|---|---|
| `ADDR` | `:8080` | Listen address |
| `PUBLIC_URL` | from request | Base URL for invite links, e.g. `https://meet.socialagent.fun` |
| `HOME_URL` | `https://socialagent.fun` | "Powered by" link |
| `MAX_PEERS` | `4` | People per room |
| `CREATE_PER_MIN` / `CREATE_PER_HOUR` | `6` / `30` | Room creations per IP |
| `TRUST_PROXY` | `1` | Use `X-Real-Ip`/`X-Forwarded-For` from the reverse proxy |
| `STUN_URLS` | Google + Cloudflare STUN | Comma-separated |
| `TURN_URLS`, `TURN_SECRET` | empty | External TURN with a shared secret (coturn `use-auth-secret`) |
| `TURN_EMBEDDED` | `0` | `1` starts pion/turn on UDP `TURN_PORT` (3478) |
| `TURN_PUBLIC_IP`, `TURN_RELAY_MIN`, `TURN_RELAY_MAX` | -, 49160, 49200 | Embedded TURN public IP and relay ports (publish these UDP ports) |

TURN credentials are short-lived (6 h) TURN REST API credentials minted per
websocket join; the secret never leaves the server.

## Release

Semver in `VERSION`, an entry in `CHANGELOG.md`, a PR with green CI, merge,
then tag `meet/vX.Y.Z`.
