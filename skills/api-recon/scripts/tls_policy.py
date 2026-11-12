"""Verified TLS by default, with an explicit exception for one target origin."""
import ssl
import sys
import urllib.parse
import urllib.request


def origin_of(raw):
    try:
        url = urllib.parse.urlsplit(raw)
        scheme = url.scheme.lower()
        if scheme not in ("http", "https") or not url.hostname or url.username is not None or url.password is not None:
            return None
        return (scheme, url.hostname.lower(), url.port if url.port is not None else (443 if scheme == "https" else 80))
    except ValueError:
        return None


class TargetTLS:
    def __init__(self, base=None, insecure=False, cafile=None):
        self.secure = ssl.create_default_context()
        if cafile:
            self.secure.load_verify_locations(cafile=cafile)
        self.approved_origin = origin_of(base) if base else None

        if base is not None and self.approved_origin is None:
            raise ValueError("base must be an HTTP(S) URL without embedded credentials")
        self.exception = None
        if insecure:
            if self.approved_origin is None or self.approved_origin[0] != "https":
                raise ValueError("--insecure requires an HTTPS base URL without embedded credentials")
            self.exception = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            self.exception.check_hostname = False
            self.exception.verify_mode = ssl.CERT_NONE

    def handler(self):
        # Each opener owns its handler; contexts are immutable and can be shared
        # by the static harvester's concurrent downloads.
        return TargetHTTPSHandler(self)


class TargetHTTPSHandler(urllib.request.HTTPSHandler):
    def __init__(self, policy):
        super().__init__(context=policy.secure)
        self.policy = policy

    def https_open(self, req):
        context = self.policy.secure
        if self.policy.exception is not None and origin_of(req.full_url) == self.policy.approved_origin:
            context = self.policy.exception
        # Choose again for every URL, including redirects and foreign bundles.
        return urllib.request.HTTPSHandler(context=context).https_open(req)


DEFAULT_TLS = TargetTLS()


def add_tls_arguments(parser):
    options = parser.add_mutually_exclusive_group()
    options.add_argument("--ca-file", help="Add a PEM CA file to platform trust for verified private-PKI targets")
    options.add_argument("--insecure", action="store_true", help="Skip TLS verification only for the exact HTTPS base origin")


def tls_from_args(parser, args, base):
    try:
        policy = TargetTLS(base, args.insecure, args.ca_file)
    except (ValueError, OSError, ssl.SSLError) as error:
        parser.error(str(error))
    if args.insecure:
        print("[!] TLS verification disabled only for the exact HTTPS base origin", file=sys.stderr)
    return policy


def report_tls_failure(error):
    if isinstance(getattr(error, "reason", error), ssl.SSLError):
        print("[!] response omitted: TLS certificate validation failed; use a trusted CA or an explicit base-origin exception", file=sys.stderr)
