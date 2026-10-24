#!/usr/bin/env python3
"""Check a running HTTP server without following redirects; uses only the stdlib."""

import concurrent.futures
import json
import os
import time
import urllib.error
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    base_url = os.environ.get("SMOKE_BASE_URL", "http://127.0.0.1:8082").rstrip("/")
    client = urllib.request.build_opener(NoRedirect)

    def request(path, body=None, method=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(base_url + path, data=data, method=method)
        if body is not None:
            req.add_header("Content-Type", "application/json")
        try:
            response = client.open(req, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            return response.status, response.headers, response.read()

    deadline = time.monotonic() + 90
    while True:
        try:
            status, _, _ = request("/ready")
            if status == 200:
                break
        except (urllib.error.URLError, TimeoutError):
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError("service did not become ready within 90 seconds")
        time.sleep(1)

    status, _, _ = request("/health")
    assert status == 200, f"health returned {status}"

    payload = {
        "client_id": "ci-smoke",
        "external_id": str(uuid.uuid4()),
        "url": "https://example.com/article?source=ci",
    }
    status, headers, body = request("/urls", payload)
    assert status == 201, (status, body)
    created = json.loads(body)
    assert headers["Location"] == created["short_url"]

    status, _, body = request("/urls", payload)
    assert status == 200, (status, body)
    assert json.loads(body) == created, "duplicate changed the code or TTL"

    status, _, _ = request("/urls", dict(payload, url="https://other.example"))
    assert status == 409, f"conflict returned {status}"

    for method in ("GET", "HEAD"):
        status, headers, body = request("/" + created["code"], method=method)
        assert status == 302, (method, status)
        assert headers["Location"] == payload["url"]
        assert headers["Cache-Control"] == "no-store"
        if method == "HEAD":
            assert not body

    status, _, _ = request("/invalid-code")
    assert status == 404

    parallel = dict(payload, external_id=str(uuid.uuid4()))
    with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:
        responses = list(pool.map(lambda _: request("/urls", parallel), range(10)))
    statuses = [response[0] for response in responses]
    assert statuses.count(201) == 1 and statuses.count(200) == 9, statuses
    assert len({json.loads(response[2])["code"] for response in responses}) == 1
    print("Smoke checks passed: readiness, create, reuse, conflict, GET/HEAD, 404, concurrency")


if __name__ == "__main__":
    main()
