# Fabric

Fabric is a small, focused server for Python WSGI apps.
One binary in Go, one clear process model, and almost no moving parts.

## About

This project was built for people who want reliable local and production behavior without ceremony.
The Go layer owns sockets, HTTP, static serving, logging, and process lifecycle.
The Python layer stays focused on your app code.

You get a clean handoff between services, sensible defaults, and explicit options when you want control.

## Links

- [Fabric SPA / Landing Page](https://fabric.jasencarroll.com)

## Install

### macOS / Linux

```bash
curl -fsSL fabric.jasencarroll.com/install.sh | sh
```

### Windows (PowerShell)

```powershell
irm fabric.jasencarroll.com/install.ps1 | iex
```

### From source

```bash
go build -o fabric ./cmd/fabric/
```

## Use

### Run your app

```bash
fabric run app.py
```

```python
# app.py
def app(environ, start_response):
    start_response("200 OK", [("Content-Type", "text/html")])
    return [b"<h1>Hello from Fabric</h1>"]
```

```bash
fabric run app.py
# open http://localhost:3000
```

### Works with any WSGI app

```python
# app.py
from flask import Flask
app = Flask(__name__)

@app.route("/")
def index():
    return "<h1>Hello from Flask via Fabric</h1>"
```

## How it works

```text
Browser -> Go net/http (port 3000) -> Unix socket -> Python worker -> your app
```

Fabric runs a Go reverse proxy and process manager.
The Python side is launched with `wsgiref` using `ThreadingMixIn` and communicates with Go via the Unix socket.

- In dev mode, Fabric watches `.py` and `.html` files and restarts cleanly on changes.
- On worker crash, it restarts the worker with loop protection.
- In `--prod`, it disables the watcher and uses structured logs.

Zero external Go dependencies. Zero external Python dependencies for the server binary.

## CLI reference

```text
fabric run <app.py>                 run a WSGI app (dev mode)
fabric run app.py --prod             production mode (JSON logs, no watcher)
fabric run app.py --port 8080         custom port (default 3000)
fabric run app.py --static .         serve static files at /static/
fabric run app.py --db ./data         SQLite path (sets FABRIC_DB env var)
fabric version                       print version
fabric help                          show help
```

## Demo

```bash
cd examples/demo
uv sync    # or: pip install flask
fabric run app.py
```

Then open `http://localhost:3000`.

That demo app uses Flask, Jinja2 templates, SQLite, and `bone.css`.

## What it intentionally does not do

- Scaffold projects
- Manage Python packages
- Install Python
- Deploy to cloud platforms
- Pick your architecture
- Impose a specific coding style

## License

BSD 3-Clause. See [LICENSE](LICENSE).
