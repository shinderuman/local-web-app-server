# Local Web App Server

Local Web App Server hosts small, independently installed local web
applications on macOS. The host owns LAN HTTP, process lifecycle, static
file isolation, Unix-domain-socket proxying, and local logs. Each application
owns its UI and backend.

The server deliberately has no TLS, authentication, internet exposure, or
arbitrary command API. It binds to `0.0.0.0` by default for use on a trusted
LAN. Use `--listen 127.0.0.1:8766` when loopback-only access is required.

## Build and install

Go 1.26 is required.

```sh
make
make test
make install
```

`make install` installs `local-web-app-server` into `~/.local/bin` by default.

## App directory

The default app directory is:

```text
~/Library/Application Support/LocalWebAppServer/apps
```

Each immediate child containing `local-web-app.json` is an installed app:

```text
apps/
└── example/
    ├── local-web-app.json
    ├── web/
    │   └── index.html
    └── bin/
        └── backend
```

Example manifest:

```json
{
  "schema_version": 1,
  "id": "example",
  "name": "Example",
  "web_root": "web",
  "backend": {
    "executable": "bin/backend",
    "args": [],
    "shutdown_timeout_seconds": 10
  }
}
```

`web_root` and `backend.executable` must be relative paths within the app
directory. Symlinks are rejected from the web tree. App IDs use lowercase
ASCII letters, digits, and internal hyphens.

The backend receives `LOCAL_WEB_SOCKET` and must listen on that Unix socket.
Requests under `/apps/<id>/api/...` reach the backend as `/api/...`; all other
app paths are served from `web_root`.

`shutdown_timeout_seconds` is optional and defaults to 10 seconds. A value of
zero waits indefinitely for graceful backend shutdown, allowing an application
to finish its own in-progress work.

## Run

```sh
local-web-app-server
local-web-app-server --open
local-web-app-server --stop
local-web-app-server --apps /path/to/apps --listen 127.0.0.1:8766
local-web-app-server --listen 0.0.0.0:8766
```

Every long option has a short form:

```text
-a, --apps
-l, --listen
-r, --runtime
-g, --log-directory
-o, --open
-s, --stop
```

Defaults:

- Listen address: `0.0.0.0:8766`
- Runtime data: `/private/tmp/local-web-app-server-<uid>`
- Logs: `~/Library/Logs/LocalWebAppServer`

A second launch detects the running instance, verifies its health endpoint,
prints its URL, and exits successfully. If the recorded instance no longer
responds, startup sends it a graceful termination signal, force-stops it after
five seconds if necessary, and retries startup. Backend processes start once
with the host. A failed backend is shown as unavailable without restarting it
or affecting other apps.

`--stop` sends the running host a graceful termination request and waits for
all application backends to finish their declared shutdown behavior. It is
safe to use before replacing installed application binaries.

When listening on `0.0.0.0`, the printed/status URL remains the local
`127.0.0.1` URL. Other computers use
`http://<server-mac-LAN-address>:8766/`. Work requested through an app still
runs in that app's backend process on the server Mac.

## Routes

- `/`: generic installed-app list
- `/_local/health`: host health
- `/_local/apps`: installed-app and backend status
- `/apps/<id>/`: app static UI
- `/apps/<id>/api/...`: app backend proxy, including SSE

## Security boundary

This is a trusted local or trusted-LAN application host. It has no TLS or HTTP
authentication, so `0.0.0.0` must not be used on an untrusted network or with
router port forwarding. Installed manifests and
backend executables are trusted local files, but URL paths are untrusted.
Manifest parsing rejects unknown fields, escaping paths, web symlinks,
non-executable backends, duplicate IDs, and invalid shutdown timeouts. There is
no HTTP route that starts arbitrary processes.

## License

MIT
