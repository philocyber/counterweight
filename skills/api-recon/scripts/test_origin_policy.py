import os
import pathlib
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SCRIPT = pathlib.Path(os.environ.get("SPIDER_UNDER_TEST", pathlib.Path(__file__).with_name("spider_mpa.py")))

class CrawlerCredentialBoundary(unittest.TestCase):
    def setUp(self):
        self.approved_requests, self.foreign_requests = [], []
        fixture = self

        class Foreign(BaseHTTPRequestHandler):
            def do_GET(self):
                fixture.foreign_requests.append((self.path, self.headers.get("Cookie")))
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.end_headers()
                self.wfile.write(b"<html>foreign fixture</html>")
            def log_message(self, *_):
                pass

        class Approved(BaseHTTPRequestHandler):
            def do_GET(self):
                fixture.approved_requests.append((self.path, self.headers.get("Cookie")))
                if self.path == "/redirect":
                    self.send_response(302)
                    self.send_header("Location", fixture.foreign + "/sink")
                    self.end_headers()
                    return
                if self.path == "/same-redirect":
                    self.send_response(302)
                    self.send_header("Location", "/good")
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header("Content-Type", "text/html")
                self.end_headers()
                if self.path == "/links":
                    self.wfile.write(f'<a href="/good">good</a><a href="{fixture.foreign}/sink">foreign</a>'.encode())
                else:
                    self.wfile.write(b'<form action="/api/fixture" method="post"><input name="safe"></form>')
            def log_message(self, *_):
                pass

        # The foreign port has the approved port as a textual prefix. Both are
        # genuinely loopback listeners; no hosts-file changes or DNS are needed.
        for port in range(5000, 5010):
            try:
                approved = ThreadingHTTPServer(("127.0.0.1", port), Approved)
            except OSError:
                continue
            try:
                foreign = ThreadingHTTPServer(("127.0.0.1", int(str(port) + "1")), Foreign)
            except OSError:
                approved.server_close()
                continue
            break
        else:
            self.fail("cannot allocate loopback origin-prefix fixture ports")
        self.base = f"http://127.0.0.1:{approved.server_port}"
        self.foreign = f"http://127.0.0.1:{foreign.server_port}"
        for server in (approved, foreign):
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            self.addCleanup(server.server_close)
            self.addCleanup(server.shutdown)

    def crawl(self, route):
        with tempfile.TemporaryDirectory() as output:
            result = subprocess.run([sys.executable, str(SCRIPT), self.base + route, output,
                                     "--cookie", "fixture_session=synthetic-only", "--max", "8", "--depth", "1", "--delay", "0"],
                                    capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stderr)
            forms = pathlib.Path(output, "forms.txt").read_text()
        self.assertTrue(self.approved_requests)
        self.assertTrue(all(cookie == "fixture_session=synthetic-only" for _, cookie in self.approved_requests))
        self.assertEqual(self.foreign_requests, [], "crawler reached a foreign origin with credentials")
        return forms

    def test_url_prefix_is_not_origin_authorization(self):
        self.assertTrue(self.foreign.startswith(self.base))
        self.assertIn("POST /api/fixture", self.crawl("/links"))
        self.assertTrue(any(path == "/good" for path, _ in self.approved_requests))

    def test_foreign_redirect_is_rejected_before_request(self):
        self.crawl("/redirect")

    def test_same_origin_redirect_keeps_authenticated_crawl(self):
        self.assertIn("POST /api/fixture", self.crawl("/same-redirect"))

if __name__ == "__main__":
    unittest.main()
