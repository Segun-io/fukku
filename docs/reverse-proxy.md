# Reverse proxy

Fukku has no log-in page. A reverse proxy in front of it signs users in (with whatever you
already use: a forward-authentication service, single sign-on, basic auth) and tells Fukku who
they are in request headers. This page says what Fukku needs from any proxy, then gives worked
configurations for Caddy, Traefik and nginx. All names are placeholders:
`fukku.example.com` is the address your users type, `auth` is your forward-authentication
service.

## What Fukku needs from any proxy

1. **Authenticate every request** before it reaches Fukku, and answer an unauthenticated
   browser with a redirect to your log-in page. (Fukku itself answers `GET /healthz` without a
   user, for uptime monitors; nothing else.)
2. **Set the user headers, and remove them from what the client sent.** Fukku trusts these
   headers completely, so a client must never be able to supply them.

   | Header (default) | Setting | Needed |
   | --- | --- | --- |
   | `Remote-User` | `auth.userHeader` | yes: the user name. Exactly one value, at most 256 bytes, UTF-8, no control characters, or the request gets 403 |
   | `Remote-Groups` | `auth.groupsHeader` (empty by default: no groups header is read) | no: groups separated by `auth.groupsSeparator` (default `,`); members of `auth.adminGroup` are admins. Set `auth.groupsHeader: Remote-Groups` only when the proxy sets it on every request and drops the client's own (the examples below do); `auth.adminGroup` needs it |
   | `Remote-Email` | `auth.emailHeader` | no: shown in the audit log |

   Use other header names by changing the settings. If your auth service names its answers
   differently (`X-Forwarded-User`, ...), map them onto these names in the proxy. If you do not
   send groups, leave `auth.groupsHeader` empty and set `auth.allUsersAreAdmins: true` (a small
   trusted household) or nobody is an admin. Fukku reads no header that is not named in a
   setting, so a client's `Remote-Groups` does nothing while `auth.groupsHeader` is empty.
3. **Connect from a known address.** Fukku accepts a request only from `auth.trustedProxies`
   (addresses or CIDRs), judged by the address the connection came from. `X-Forwarded-For` is
   never read. Anything else gets 403 and is logged as `auth_refused`. Trust the proxy's own
   address, never a range that other containers or users share. A prefix shorter than /8 (IPv4)
   or /32 (IPv6), such as `0.0.0.0/0`, is refused at start-up. How to arrange that with Docker
   is below.
4. **Pass the request's `Origin` and `Host` through unchanged.** Every change (`POST`, `PATCH`,
   `DELETE`) must carry an `Origin` equal to `server.publicBaseUrl`, scheme and port included
   (when that is empty, the Origin's host must equal the request's `Host`, which a page that
   rebinds DNS controls: `fukku check-config` warns). Set
   `server.publicBaseUrl` to exactly what the browser shows, `https://fukku.example.com`, and
   keep the proxy's TLS at the proxy: Fukku speaks plain HTTP on port 8080.
5. **Allow what uploads use.** Resumable uploads (the tus protocol) use `POST`, `HEAD`, `PATCH`
   and `DELETE` on `/api/uploads/`, with the headers `Tus-Resumable`, `Upload-Length`,
   `Upload-Offset`, `Upload-Metadata` and `Content-Type: application/offset+octet-stream`
   passed both ways, and a `Location` header in the answer that must not be rewritten (Fukku
   builds it from `server.publicBaseUrl`). Do not restrict methods, and do not add a second
   Origin or CORS policy.
6. **A request timeout above one chunk.** One `PATCH` carries up to `upload.chunkSize` (16 MiB by
   default): at 1 Mbit/s that is over two minutes. Set the proxy's timeouts for reading the
   client's body and for the upstream to several minutes, or lower `upload.chunkSize`. An
   upload that stops at the same size, or at a round number of seconds, is a timeout
   ([troubleshooting.md](troubleshooting.md)). Nothing in Fukku streams events or sockets.
7. **No body buffering and a body limit above one chunk.** Chunks should stream through. Some
   proxies buffer the whole body to disk or refuse bodies above a default (nginx: 1 MB), and
   some content-delivery networks cap a request at 100 MB: keep `upload.chunkSize` below that.
   There is no need for a limit on the whole upload, because no request is larger than one chunk.
8. **Do not cache** the API, and let `Content-Security-Policy` and the other security headers
   Fukku sets through.
9. **Do not publish Fukku's own port.** The web container only `expose`s 8080 on a network the
   proxy joins.

`GET /readyz` (every readiness check) and `GET /metrics` (when `server.metrics` is on) need an
admin through the proxy, or a connection from a loopback address of the Fukku container. Point an
uptime monitor at `GET /healthz`.

## Docker: a fixed address for the proxy

The compose file ([deploy/compose.example.yaml](../deploy/compose.example.yaml)) creates a
network `fukku-frontend` with the subnet `172.29.50.0/24`, joined by the web container (as
`fukku-web`) and nothing else, and `deploy/fukku.example.yaml` trusts `172.29.50.10`. Run the
proxy as a container on that network, at that address. In the proxy's own compose file:

```yaml
services:
  proxy:
    # ... image, ports 80 and 443, volumes ...
    networks:
      fukku-frontend:
        ipv4_address: 172.29.50.10
      # and whatever network reaches your auth service
networks:
  fukku-frontend:
    external: true
```

Start Fukku first (it creates the network). Change the subnet, the address and
`auth.trustedProxies` together if that range is taken on your host.

If the proxy runs on the host instead, publish Fukku to loopback
(`ports: ["127.0.0.1:8080:8080"]` on the web service, and `127.0.0.1:8080` as the upstream).
Fukku then sees every connection from the gateway of the Docker network, which this prints:

```sh
docker network inspect fukku-frontend --format '{{(index .IPAM.Config 0).Gateway}}'
```

Put that address in `auth.trustedProxies`. Anyone who can connect to that loopback port on the
host can then supply the headers, so only do this on a host without other users. When in doubt,
send one request and read the `auth_refused` line in `docker compose logs web`: it names the
address the connection came from.

## Caddy

Tested with Caddy 2.11.9 (`caddy:2.11.9`). `Caddyfile`:

```caddyfile
fukku.example.com {
	# Sign the user in with your forward-auth service. It answers 2xx for a signed-in user
	# (and the headers below), and anything else (a redirect to its login page, 401) goes back
	# to the browser.
	forward_auth auth:9091 {
		uri /verify
		copy_headers Remote-User Remote-Email Remote-Groups
	}

	reverse_proxy fukku-web:8080 {
		# Uploads: chunks stream as they arrive (the default), and a slow one is not given up on
		# while Fukku is still reading it.
		transport http {
			read_timeout 10m
			write_timeout 10m
		}
	}
}
```

`forward_auth` asks `http://auth:9091/verify` first, copies the three headers from its answer
onto the request (and removes any the client sent), and sends everything else to Fukku. Caddy
gets the certificate for `fukku.example.com` by itself. Change the left-hand header names if your
service names them differently, and `uri` to its verification path (see its documentation).

```sh
docker run --rm -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" caddy:2.11.9 caddy validate --config /etc/caddy/Caddyfile
```

## Traefik

Tested with Traefik 3.7.19 (`traefik:v3.7.19`). Traefik reads a static file (`traefik.yml`) and
the file provider's dynamic file (`dynamic.yml`).

`traefik.yml`:

```yaml
entryPoints:
  websecure:
    address: ":443"
    transport:
      respondingTimeouts:
        # Traefik cuts a request that takes longer than this to arrive, and the default is 60s:
        # raise it above the time one upload chunk needs on the slowest link.
        readTimeout: 10m
        writeTimeout: 10m
        idleTimeout: 3m

providers:
  file:
    filename: /etc/traefik/dynamic.yml

certificatesResolvers:
  letsencrypt:
    acme:
      email: admin@example.com
      storage: /acme/acme.json
      tlsChallenge: {}
```

`dynamic.yml`:

```yaml
http:
  routers:
    fukku:
      rule: Host(`fukku.example.com`)
      entryPoints: [websecure]
      service: fukku
      # Order matters: remove what the client sent, then sign in (which sets the headers).
      middlewares: [fukku-strip-identity, fukku-auth]
      tls:
        certResolver: letsencrypt

  middlewares:
    fukku-strip-identity:
      headers:
        customRequestHeaders:
          Remote-User: ""
          Remote-Email: ""
          Remote-Groups: ""
    fukku-auth:
      forwardAuth:
        address: http://auth:9091/verify
        authResponseHeaders: [Remote-User, Remote-Email, Remote-Groups]

  services:
    fukku:
      loadBalancer:
        servers:
          - url: http://fukku-web:8080
```

Traefik has no syntax validator. To check the files, run it with them (in a pinned container) and
read the log: it names the file and line of an error, and it reports no error besides the
certificate request when `fukku.example.com` is not yours.

```sh
docker run --rm -v "$PWD/traefik.yml:/etc/traefik/traefik.yml:ro" -v "$PWD/dynamic.yml:/etc/traefik/dynamic.yml:ro" -v "$PWD/acme:/acme" traefik:v3.7.19
```

Traefik's `readTimeout` is the one that cuts slow uploads: its default is 60 s per request.

## nginx

Tested with nginx 1.31.8 (`nginx:1.31.8`). A file in `/etc/nginx/conf.d/`, `fukku.conf`:

```nginx
server {
    listen 80;
    server_name fukku.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name fukku.example.com;

    ssl_certificate     /etc/nginx/tls/fullchain.pem;
    ssl_certificate_key /etc/nginx/tls/privkey.pem;

    # Uploads: a body limit above upload.chunkSize (the nginx default, 1m, would refuse every
    # chunk), and no buffering of the body to disk.
    client_max_body_size 64m;
    proxy_request_buffering off;
    proxy_buffering off;
    # Longer than one chunk needs on the slowest link (nginx's default is 60s).
    client_body_timeout 10m;
    proxy_send_timeout 10m;
    proxy_read_timeout 10m;

    # Every request is checked by the forward-auth service first.
    location = /_auth {
        internal;
        proxy_pass http://auth:9091/verify;
        proxy_pass_request_body off;
        proxy_set_header Content-Length "";
        proxy_set_header X-Original-URI $request_uri;
        proxy_set_header X-Original-Host $host;
    }

    location / {
        auth_request /_auth;
        # An unauthenticated user goes to the login page of your auth service.
        error_page 401 = @login;

        # What the auth service answered. proxy_set_header replaces whatever the client sent,
        # and a header whose value is empty is not sent at all.
        auth_request_set $remote_user_value   $upstream_http_remote_user;
        auth_request_set $remote_email_value  $upstream_http_remote_email;
        auth_request_set $remote_groups_value $upstream_http_remote_groups;
        proxy_set_header Remote-User   $remote_user_value;
        proxy_set_header Remote-Email  $remote_email_value;
        proxy_set_header Remote-Groups $remote_groups_value;

        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_pass http://fukku-web:8080;
    }

    location @login {
        return 302 https://auth.example.com/?rd=$scheme://$host$request_uri;
    }
}
```

Certificates are yours to provide (here `/etc/nginx/tls/`, from your ACME client). nginx
resolves `auth` and `fukku-web` when it starts, so both must be reachable from the proxy's
network then. The default body limit of 1 MB is the usual reason an nginx upload fails at once
(`413`).

```sh
docker run --rm --network fukku-frontend -v "$PWD/fukku.conf:/etc/nginx/conf.d/fukku.conf:ro" -v "$PWD/tls:/etc/nginx/tls:ro" nginx:1.31.8 nginx -t
```

## Check that it works

Sign in through the proxy and open `https://fukku.example.com/api/session`: it answers with your
user name and `"admin": true` when your group is the admin group. Then:

- A request that skips the proxy is refused: from another container, `curl -i -H 'Remote-User:
  anyone' http://fukku-web:8080/api/session` answers `403` and the web log gets an
  `auth_refused` line with `reason=untrusted_address`.
- A header the client sends does not count: send `Remote-Email: someone@example.com` through
  the proxy to `/api/session` and the answer's email is the one your auth service set (or empty).
  Do the same with `Remote-Groups: media-admins` while signed in as a user who is not in that
  group: `"admin"` must stay `false`. If it turns `true`, the proxy passes the client's groups
  through; fix it, or leave `auth.groupsHeader` empty.
- A full chunk gets through: an upload bigger than `upload.chunkSize` (16 MiB) goes past its first
  chunk instead of stopping there.

## Validation of these examples

Each configuration above was run in a pinned container on a network with the Fukku web container
and a stub forward-auth service, and driven with real requests: `GET /api/session` with a spoofed
`Remote-Email` (it did not arrive), a `POST` without `Origin` (403), creating a job and an
upload, a 16 MiB `PATCH` and a `HEAD` of the upload. Caddy was checked with `caddy validate`,
nginx with `nginx -t`. Traefik has no validator and was checked by running it. For the test runs
only, Caddy got `tls internal`, nginx a self-signed certificate and Traefik its default
certificate, because `example.com` cannot get a real one.

The Caddy example is also exercised by the clean-room install test (`make install-test`,
[development.md](development.md#the-clean-room-install-test)): the Caddyfile is taken from this
page, with `tls internal` added, and put in front of a fresh install with a stub forward-auth
service, which signs a user in and sets the three headers. Through it the test checks the
redirect of an unauthenticated request, `/api/session` for an admin and an ordinary user, that a
client's `Remote-Groups` and `Remote-Email` do not count, the 403 of a request that skips the
proxy, and uploads of several chunks.
