"""Shared byte and work budgets for the stdlib reconnaissance readers."""
import threading
import time


class ResponseLimitError(Exception):
    pass


class ResponseBudget:
    def __init__(self, max_response_bytes=8 * 1024 * 1024,
                 max_total_bytes=256 * 1024 * 1024, max_requests=2048,
                 response_seconds=25):
        if min(max_response_bytes, max_total_bytes, max_requests, response_seconds) <= 0:
            raise ValueError("response budgets must be positive")
        self.max_response_bytes = max_response_bytes
        self.max_total_bytes = max_total_bytes
        self.max_requests = max_requests
        self.response_seconds = response_seconds
        self.total_bytes = 0
        self.requests = 0
        self._lock = threading.Lock()

    def begin(self):
        with self._lock:
            if self.requests >= self.max_requests or self.total_bytes >= self.max_total_bytes:
                raise ResponseLimitError("aggregate reconnaissance budget exhausted")
            self.requests += 1

    def read(self, response):
        deadline = time.monotonic() + self.response_seconds
        data = bytearray()
        # read1 returns available bytes instead of waiting to fill a large read.
        # The opener's socket timeout also bounds an idle read; check elapsed
        # time between reads so a continuously trickling body cannot run forever.
        read = getattr(response, "read1", response.read)
        while True:
            if time.monotonic() >= deadline:
                raise ResponseLimitError("response read deadline exceeded")
            with self._lock:
                remaining = self.max_total_bytes - self.total_bytes
                if remaining <= 0:
                    raise ResponseLimitError("aggregate response byte budget exhausted")
                size = min(65536, self.max_response_bytes - len(data) + 1, remaining)
                # Reserve before reading: concurrent downloads cannot all spend
                # the same remaining allowance. Refund unused bytes at EOF.
                self.total_bytes += size
            try:
                chunk = read(size)
            except BaseException:
                with self._lock:
                    self.total_bytes -= size
                raise
            with self._lock:
                self.total_bytes -= size - len(chunk)
            if not chunk:
                return bytes(data)
            if len(data) + len(chunk) > self.max_response_bytes:
                raise ResponseLimitError("response byte budget exceeded")
            data.extend(chunk)


def add_budget_arguments(parser):
    def positive(value):
        result = int(value)
        if result <= 0:
            raise ValueError("budget must be positive")
        return result
    parser.add_argument("--max-response-bytes", type=positive, default=8 * 1024 * 1024)
    parser.add_argument("--max-total-bytes", type=positive, default=256 * 1024 * 1024)
    parser.add_argument("--max-requests", type=positive, default=2048)


def budget_from_args(args):
    return ResponseBudget(args.max_response_bytes, args.max_total_bytes, args.max_requests)
