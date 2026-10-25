from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"ok\n"
        if self.path == "/license":
            # only when it is asked: a file of the distribution that nothing opens before
            with open("/usr/share/common-licenses/GPL-3", "rb") as f:
                body = f.readline()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


HTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
