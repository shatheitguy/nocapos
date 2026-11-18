#!/usr/bin/env python3
"""serve.py DIR PORT: a file server with HTTP range requests, like GitHub's
release downloads, for the update test (Rugix fetches only the blocks it
needs, with ranges)."""
import http.server
import os
import re
import sys


class Handler(http.server.SimpleHTTPRequestHandler):
    # Keep-alive, like a real download server: Rugix reuses connections.
    protocol_version = "HTTP/1.1"
    def send_head(self):
        path = self.translate_path(self.path)
        if not os.path.isfile(path):
            return super().send_head()
        size = os.path.getsize(path)
        m = re.match(r"bytes=(\d*)-(\d*)$", self.headers.get("Range", ""))
        f = open(path, "rb")
        if not m:
            self.send_response(200)
            self.send_header("Content-Length", str(size))
            self.send_header("Accept-Ranges", "bytes")
            self.send_header("Content-Type", "application/octet-stream")
            self.end_headers()
            return f
        start = int(m.group(1)) if m.group(1) else max(0, size - int(m.group(2)))
        end = int(m.group(2)) if m.group(1) and m.group(2) else size - 1
        end = min(end, size - 1)
        if start > end:
            f.close()
            self.send_error(416)
            return None
        f.seek(start)
        self.send_response(206)
        self.send_header("Content-Range", f"bytes {start}-{end}/{size}")
        self.send_header("Content-Length", str(end - start + 1))
        self.send_header("Accept-Ranges", "bytes")
        self.send_header("Content-Type", "application/octet-stream")
        self.end_headers()
        self._remaining = end - start + 1
        return f

    def copyfile(self, source, outputfile):
        left = getattr(self, "_remaining", None)
        if left is None:
            return super().copyfile(source, outputfile)
        while left > 0:
            chunk = source.read(min(1 << 20, left))
            if not chunk:
                break
            outputfile.write(chunk)
            left -= len(chunk)
        self._remaining = None



if __name__ == "__main__":
    os.chdir(sys.argv[1])
    http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[2])), Handler).serve_forever()
