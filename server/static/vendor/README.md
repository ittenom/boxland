# Vendored client JS

| File | Source | Version |
|---|---|---|
| `htmx.min.js` | https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js | 2.0.4 |
| `htmx-ext-ws.min.js` | https://unpkg.com/htmx-ext-ws@2.0.2/ws.js | 2.0.2 |
| `alpine.min.js` | https://unpkg.com/alpinejs@3.14.7/dist/cdn.min.js | 3.14.7 |

Bumped manually; verified against upstream changelog before each bump.

We vendor instead of CDN-loading so:
- Local dev works offline.
- We can audit / pin CSP-friendly versions.
- The single Boxland binary ships with everything it needs.
