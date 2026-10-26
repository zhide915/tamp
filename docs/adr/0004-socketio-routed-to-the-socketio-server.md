# /socket.io is routed to the socket.io server

A bench serves the web app on port 8000 and a socket.io server on port 9000. The browser opens its realtime and live-reload connections to `/socket.io` on the page's own hostname, so the router sends each site's `/socket.io*` to port 9000 and everything else to port 8000, and publishes only port 80. A running bench showed on 2026-10-27 that this works only when the bench runs without `DEV_SERVER`, the router sets `Origin` on `/socket.io`, and the bench settings set `webserver_host` to `127.0.0.1`.

## Considered options

- Keep `DEV_SERVER` and publish port 9000 on the router as well: a second published port.
- Set `socketio_port: 80` in each site's config: the router's port in every site, through an undocumented config precedence.

## Consequences

- Without `DEV_SERVER`, some website routes stay cached until `bench clear-website-cache`, and deprecation warnings are hidden.
- Setting `Origin` turns off the realtime server's cross-site check, as bench's own nginx template does. It suits a development router only.
