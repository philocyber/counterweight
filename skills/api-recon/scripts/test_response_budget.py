import concurrent.futures
import io
import pathlib
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from response_budget import ResponseBudget, ResponseLimitError


class ResponseBudgets(unittest.TestCase):
    def test_boundary_and_overflow_never_use_unbounded_read(self):
        class Reader(io.BytesIO):
            def read1(self, size):
                self.assert_size(size)
                return super().read1(size)
            def assert_size(self, size):
                if size <= 0 or size > 65536:
                    raise AssertionError(f"unbounded read: {size}")
        for length in (63, 64, 65, 1000000):
            budget = ResponseBudget(64, 1024)
            budget.begin()
            reader = Reader(b"x" * length)
            if length <= 64:
                self.assertEqual(budget.read(reader), b"x" * length)
            else:
                with self.assertRaises(ResponseLimitError): budget.read(reader)
                self.assertEqual(reader.tell(), 65)

    def test_parallel_downloads_share_one_byte_and_request_budget(self):
        budget = ResponseBudget(64, 256, 12)
        def download(_):
            try:
                budget.begin()
                return budget.read(io.BytesIO(b"x" * 64))
            except ResponseLimitError:
                return b""
        with concurrent.futures.ThreadPoolExecutor(max_workers=24) as pool:
            bodies = list(pool.map(download, range(100)))
        self.assertLessEqual(budget.total_bytes, 256)
        self.assertLessEqual(sum(map(len, bodies)), 256)
        self.assertLessEqual(budget.requests, 12)
        self.assertTrue(any(bodies))

    def test_continuously_available_body_obeys_deadline(self):
        import time
        class Trickle:
            def read(self, _):
                time.sleep(0.01)
                return b"x"
        budget = ResponseBudget(1000, 2000, response_seconds=0.025)
        budget.begin()
        with self.assertRaisesRegex(ResponseLimitError, "deadline"):
            budget.read(Trickle())
        self.assertLess(budget.total_bytes, 10)

    def test_actual_static_and_mpa_scripts_keep_valid_discovery(self):
        calls = []
        class Fixture(BaseHTTPRequestHandler):
            def do_GET(self):
                calls.append(self.path)
                self.send_response(200)
                self.send_header("Content-Type", "text/html" if self.path in ("/", "/mpa", "/oversized-page") else "application/javascript")
                self.end_headers()
                body = {
                    "/": b'<script src="/normal.js"></script><script src="/oversized.js"></script>',
                    "/normal.js": b'let api="/api/normal"; let r={path:"/dashboard"}; let p={2:"abcdef"}[x]+".js";',
                    "/assets/2.abcdef.js": b'let api="/api/from-manifest";',
                    "/2.abcdef.js": b'let api="/api/from-manifest";',
                    "/mpa": b'<a href="/oversized-page">large</a><form action="/api/form"><input name="q"></form>',
                    "/oversized-page": b'<form action="/api/oversized-form"></form>' + b'x' * 1024,
                    "/oversized.js": b'let bad="/api/oversized";' + b'x' * 1024,
                }.get(self.path, b'')
                try: self.wfile.write(body)
                except (BrokenPipeError, ConnectionResetError): pass
            def log_message(self, *_): pass
        server = ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        base = f"http://127.0.0.1:{server.server_port}"
        scripts = pathlib.Path(__file__).parent
        for script, route in (("harvest_static.py", "/"), ("spider_mpa.py", "/mpa")):
            with self.subTest(script=script), tempfile.TemporaryDirectory() as output:
                command = [sys.executable, str(scripts / script), base + route, output,
                           "--max-response-bytes", "256", "--max-total-bytes", "4096", "--max-requests", "20"]
                if script == "spider_mpa.py": command += ["--delay", "0"]
                result = subprocess.run(command, capture_output=True, text=True, timeout=15)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("response byte budget exceeded", result.stderr)
                if script == "harvest_static.py":
                    apis = pathlib.Path(output, "api_static.txt").read_text()
                    self.assertIn("/api/normal", apis)
                    self.assertIn("/api/from-manifest", apis)
                    self.assertNotIn("/api/oversized", apis)
                    self.assertFalse(pathlib.Path(output, "js/oversized.js").exists())
                    self.assertIn("/dashboard", pathlib.Path(output, "routes.txt").read_text())
                else:
                    forms = pathlib.Path(output, "forms.txt").read_text()
                    self.assertIn("GET /api/form", forms)
                    self.assertNotIn("oversized-form", forms)


if __name__ == "__main__":
    unittest.main()
