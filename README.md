# Local Web App Server

Local Web App Server hosts small, independently installed local web
applications on macOS. The host owns loopback HTTP, process lifecycle, static
file isolation, Unix-domain-socket proxying, and local logs. Each application
owns its UI and backend.

The server deliberately has no TLS, authentication, LAN mode, internet
exposure, or arbitrary command API. It binds only to `127.0.0.1`.

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
local-web-app-server --apps /path/to/apps --listen 127.0.0.1:8765
```

Every long option has a short form:

```text
-a, --apps
-l, --listen
-r, --runtime
-g, --log-directory
-o, --open
```

Defaults:

- Listen address: `127.0.0.1:8765`
- Runtime data: `/private/tmp/local-web-app-server-<uid>`
- Logs: `~/Library/Logs/LocalWebAppServer`

A second launch detects the running instance, verifies its health endpoint,
prints its URL, and exits successfully. Backend processes start once with the
host. A failed backend is shown as unavailable without restarting it or
affecting other apps.

## Routes

- `/`: generic installed-app list
- `/_local/health`: host health
- `/_local/apps`: installed-app and backend status
- `/apps/<id>/`: app static UI
- `/apps/<id>/api/...`: app backend proxy, including SSE

## Security boundary

This is a trusted, single-user local application host. Installed manifests and
backend executables are trusted local files, but URL paths are untrusted.
Manifest parsing rejects unknown fields, escaping paths, web symlinks,
non-executable backends, duplicate IDs, and invalid shutdown timeouts. There is
no HTTP route that starts arbitrary processes.

## License

MIT
