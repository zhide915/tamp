# /socket.io is routed to the socket.io server

A bench serves the web app on port 8000, and a socket.io server on port 9000. The browser opens its realtime and live-reload connections to `/socket.io` on the hostname of the page, so the router sends the `/socket.io*` of each site to port 9000 and everything else to port 8000, and publishes only port 80. This works only when the bench runs without `DEV_SERVER`, the router sets `Origin` on `/socket.io`, and the bench settings set `webserver_host` to `127.0.0.1`.

## Considered options

- Keep `DEV_SERVER`, and publish port 9000 on the router too: a second published port.
- Set `socketio_port: 80` in the config of each site: the port of the router in every site, through a config precedence that Frappe does not document.

## Consequences

- Without `DEV_SERVER`, some website routes stay cached until `bench clear-website-cache`, and deprecation warnings do not print.
- Setting `Origin` turns off the cross-site check of the realtime server, as the nginx template of bench does. It suits a development router only.
