import importlib
import os
import socket
import socketserver
import sys
from wsgiref.simple_server import WSGIRequestHandler, WSGIServer

app_ref = sys.argv[1]
socket_path = sys.argv[2]

sys.path.insert(0, os.getcwd())

module_name, _, attr = app_ref.partition(":")
attr = attr or "app"
mod = importlib.import_module(module_name)
application = getattr(mod, attr)


class UnixWSGIServer(socketserver.ThreadingMixIn, WSGIServer):
    address_family = socket.AF_UNIX
    daemon_threads = True

    def server_bind(self):
        self.server_address = self.server_address[0]
        self.socket.bind(self.server_address)
        self.server_name = "fabric-worker"
        self.server_port = 0
        self.setup_environ()

    def get_request(self):
        request, _ = self.socket.accept()
        return request, ("", 0)


class QuietHandler(WSGIRequestHandler):
    def log_request(self, *args):
        pass


server = UnixWSGIServer((socket_path,), QuietHandler)
server.set_app(application)
print("ready", flush=True)
server.serve_forever()
