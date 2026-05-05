# nginx Reverse Proxy

Igloo can run directly on its HTTP port, but nginx can sit in front of it for
HTTPS, HTTP/2, static file serving, and X-Accel media passthrough.

nginx is optional. Caddy, Traefik, Tailscale certs, Cloudflare Tunnel, or another
edge proxy can be used instead.

## Layout

```text
Browser
  -> HTTPS proxy
  -> Igloo on localhost or a private container network
```

The sample nginx setup is meant to:

- serve `/static/*` directly
- serve generated thumbnails and previews through internal X-Accel paths
- stream videos through internal X-Accel paths
- proxy everything else to the Go server

## Ports

| Port | Purpose |
|---|---|
| `5001` | Igloo HTTP server, usually localhost/private only |
| `8443` | Example nginx HTTPS port |

Use HTTPS for public or untrusted network paths. Plain HTTP is reasonable for
localhost, trusted LANs, private container networks, and VPN/tunnel access.
