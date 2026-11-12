"""Real loopback TLS tests; no external hosts or non-stdlib test dependencies."""
import contextlib
import io
import os
import pathlib
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest.mock import patch

import harvest_static
import spider_mpa
from tls_policy import TargetTLS, origin_of

HERE = pathlib.Path(__file__).parent
CERT = HERE / "test_fixtures" / "tls" / "cert.pem"
KEY = HERE / "test_fixtures" / "tls" / "key.pem"


class TargetTLSIntegration(unittest.TestCase):
    def setUp(self):
        self.proxy_env = patch.dict(os.environ, {
            "HTTP_PROXY": "", "HTTPS_PROXY": "", "http_proxy": "", "https_proxy": "",
            "NO_PROXY": "127.0.0.1,localhost", "no_proxy": "127.0.0.1,localhost",
        })
        self.proxy_env.start()
        self.addCleanup(self.proxy_env.stop)
        self.hits = []
        fixture = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                fixture.hits.append((self.server.server_port, self.path, self.headers.get("Cookie")))
                if self.path in ("/foreign", "/same"):
                    self.send_response(302)
                    self.send_header("Location", fixture.foreign + "/sink" if self.path == "/foreign" else "/good")
                    self.end_headers()
                    return
                self.send_response(200)
                self.send_header("Content-Type", "application/javascript" if self.path == "/bundle.js" else "text/html")
                self.end_headers()
                self.wfile.write(b'fetch("/api/fixture")' if self.path == "/bundle.js" else
                                 b'<script src="/bundle.js"></script><form action="/api/fixture" method="post"><input name="safe"></form>')

            def log_message(self, *_):
                pass

        servers = []
        for _ in range(2):
            server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
            context.load_cert_chain(CERT, KEY)
            server.socket = context.wrap_socket(server.socket, server_side=True)
            thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
            thread.start()
            servers.append(server)
            self.addCleanup(lambda thread=thread: thread.join(timeout=2))
            self.addCleanup(server.server_close)
            self.addCleanup(server.shutdown)
        self.base = f"https://127.0.0.1:{servers[0].server_port}"
        self.foreign = f"https://127.0.0.1:{servers[1].server_port}"

    def test_defaults_reject_untrusted_before_http_or_cookie_delivery(self):
        warnings = io.StringIO()
        with contextlib.redirect_stderr(warnings):
            self.assertEqual(harvest_static.fetch(self.base), ("", 0))
            self.assertEqual(spider_mpa.fetch(self.base, "session=fixture", origin_of(self.base)), ("", ""))
        self.assertEqual(self.hits, [])
        self.assertIn("TLS certificate validation failed", warnings.getvalue())

    def test_private_ca_preserves_verified_harvest_and_authenticated_crawl(self):
        policy = TargetTLS(self.base, cafile=str(CERT))
        html, status = harvest_static.fetch(self.base, tls_policy=policy)
        self.assertEqual(status, 200)
        self.assertIn("/bundle.js", html)
        html, content_type = spider_mpa.fetch(self.base, "session=fixture", origin_of(self.base), tls_policy=policy)
        self.assertIn("/api/fixture", html)
        self.assertEqual(content_type, "text/html")
        self.assertEqual(self.hits[-1][2], "session=fixture")

    def test_private_ca_still_rejects_hostname_mismatch(self):
        policy = TargetTLS(self.base, cafile=str(CERT))
        wrong_host = self.base.replace("127.0.0.1", "localhost")
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(harvest_static.fetch(wrong_host, tls_policy=policy), ("", 0))
        self.assertEqual(self.hits, [])

    def test_exception_is_exact_origin_across_redirects_and_downloads(self):
        policy = TargetTLS(self.base, insecure=True)
        self.assertEqual(harvest_static.fetch(self.base + "/same", tls_policy=policy)[1], 200)
        self.assertIn("/api/fixture", spider_mpa.fetch(self.base, "session=fixture", origin_of(self.base), tls_policy=policy)[0])
        before = len(self.hits)
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(harvest_static.fetch(self.foreign + "/bundle.js", tls_policy=policy), ("", 0))
            self.assertEqual(harvest_static.fetch(self.base + "/foreign", tls_policy=policy), ("", 0))
            wrong_host = self.base.replace("127.0.0.1", "localhost")
            self.assertEqual(harvest_static.fetch(wrong_host, tls_policy=policy), ("", 0))
        self.assertEqual(len(self.hits), before + 1)  # only the approved redirect issuer
        self.assertTrue(all(port == int(self.base.rsplit(":", 1)[1]) for port, _, _ in self.hits))
        self.assertEqual(spider_mpa.fetch(self.base + "/foreign", "session=fixture", origin_of(self.base), tls_policy=policy), ("", ""))
        self.assertEqual(self.hits[-1][2], "session=fixture")

    def test_cli_defaults_ca_and_explicit_exception(self):
        for script, artifact in (("harvest_static.py", "index.html"), ("spider_mpa.py", "forms.txt")):
            for flags in ([], ["--ca-file", str(CERT)], ["--insecure"]):
                with self.subTest(script=script, flags=flags), tempfile.TemporaryDirectory() as output:
                    before = len(self.hits)
                    result = subprocess.run([sys.executable, str(HERE / script), self.base, output, *flags],
                                            capture_output=True, text=True, timeout=15)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    text = (pathlib.Path(output) / artifact).read_text(encoding="utf-8")
                    if flags:
                        self.assertIn("/api/fixture" if script == "spider_mpa.py" else "/bundle.js", text)
                        self.assertGreater(len(self.hits), before)
                        if flags == ["--insecure"]:
                            self.assertIn("exact HTTPS base origin", result.stderr)
                    else:
                        self.assertEqual(text, "")
                        self.assertEqual(len(self.hits), before)
                        self.assertIn("TLS certificate validation failed", result.stderr)

    def test_invalid_exception_and_options_fail_before_request(self):
        for base in ("http://127.0.0.1:123", "https://user:password@127.0.0.1", "ftp://fixture"):
            with self.assertRaises(ValueError):
                TargetTLS(base, insecure=True)
        for script in ("harvest_static.py", "spider_mpa.py"):
            with tempfile.TemporaryDirectory() as output:
                result = subprocess.run([sys.executable, str(HERE / script), self.base, output,
                                         "--insecure", "--ca-file", str(CERT)], capture_output=True, timeout=10)
                self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.hits, [])


if __name__ == "__main__":
    unittest.main()
