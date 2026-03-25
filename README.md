# Fabric

Go server for Python WSGI apps. One binary. No Gunicorn. No nginx.

## Install

**macOS / Linux:**

```bash
curl -fsSL fabric.jasencarroll.com/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm fabric.jasencarroll.com/install.ps1 | iex
```

**From source:**

```bash
go build -o fabric ./cmd/fabric/
```

## Use

```bash
fabric run app.py
```

Write a Python WSGI app. Run it with Fabric. Open your browser.

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

Works with Flask, Django, Bottle, or any WSGI framework:

```python
# app.py
from flask import Flask
app = Flask(__name__)

@app.route("/")
def index():
    return "<h1>Hello from Flask via Fabric</h1>"
```

## Flags

```
fabric run <app.py>           run a WSGI app (dev mode)
fabric run app.py --prod      production mode (JSON logs, no watcher)
fabric run app.py --port 8080 custom port (default 3000)
fabric run app.py --static .  serve static files at /static/
fabric run app.py --db ./data SQLite path (sets FABRIC_DB env var)
fabric version                print version
fabric help                   show help
```

## How it works

Fabric is a Go binary that reverse-proxies HTTP over a Unix socket to a Python worker process. The worker runs your WSGI app using Python's stdlib `wsgiref` server with `ThreadingMixIn` for concurrent requests. Go handles HTTP, static files, logging, and worker lifecycle. Python handles your application logic.

```
Browser  -->  Go net/http (port 3000)  -->  Unix socket  -->  Python wsgiref (your app)
```

In dev mode, Fabric watches `.py` and `.html` files and restarts the worker on changes. If the worker crashes, Fabric restarts it automatically (with crash loop detection). Ctrl+C shuts everything down cleanly.

Zero external Go dependencies. Zero external Python dependencies (for the server itself).

## Demo

```bash
cd examples/demo
uv sync               # or: pip install flask
fabric run app.py
# open http://localhost:3000
```

A notes app with Flask, Jinja2 templates, SQLite, and bone.css.

## What it does not do

- Scaffold projects
- Manage Python packages
- Install Python
- Deploy
- Choose your framework
- Have opinions about your code

## License

BSD 3-Clause. See [LICENSE](LICENSE).
