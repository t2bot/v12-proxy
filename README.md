# v12-proxy
Proxy to expose creators as high power level users in v12+ rooms.

**This is not recommended for long-term use.**

## Setup

You'll need Go 1.24+ installed.

```bash
git clone https://github.com/t2bot/v12-proxy
cd v12-proxy
go build -o v12-proxy ./cmd/app/...
V12_BIND_ADDRESS=":8080" V12_DOWNSTREAM_URL="http://localhost:8008" ./v12-proxy
```

Note that the downstream URL should be direct to your homeserver. It should not be proxied through your reverse proxy.

In your reverse proxy config, route `GET /_matrix/client/{version}/rooms/{roomId}/state/m.room.power_levels` to the v12 proxy.

**Do not** route non-GET http methods or non-`m.room.power_levels` requests to the v12 proxy.
