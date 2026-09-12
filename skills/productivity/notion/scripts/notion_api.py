#!/usr/bin/env python3
"""Notion REST helper. Stdlib only. Auth: NOTION_API_KEY (or NOTION_API_TOKEN).

Usage:
  notion_api.py search "query" [--limit 10]
  notion_api.py get-page PAGE_ID
  notion_api.py create-page --parent PAGE_ID --title "Notes"
  notion_api.py raw METHOD PATH [--body JSON]
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any

API = "https://api.notion.com/v1"
VERSION = "2025-09-03"


def _key() -> str:
    key = (os.environ.get("NOTION_API_KEY") or os.environ.get("NOTION_API_TOKEN") or "").strip()
    if not key:
        sys.stderr.write("ERROR: NOTION_API_KEY not set.\n")
        sys.exit(2)
    return key


def request(method: str, path: str, body: dict[str, Any] | None = None) -> Any:
    url = API + path if path.startswith("/") else API + "/" + path
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(
        url,
        data=data,
        method=method.upper(),
        headers={
            "Authorization": "Bearer " + _key(),
            "Notion-Version": VERSION,
            "Content-Type": "application/json",
            "User-Agent": "covo-agent-notion/1.0",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        sys.stderr.write(f"HTTP {e.code}: {e.read().decode('utf-8', 'replace')}\n")
        sys.exit(1)
    except urllib.error.URLError as e:
        sys.stderr.write(f"Network error: {e}\n")
        sys.exit(1)


def cmd_search(args: argparse.Namespace) -> None:
    body = {"query": args.query, "page_size": args.limit}
    print(json.dumps(request("POST", "/search", body), indent=2, ensure_ascii=False))


def cmd_get_page(args: argparse.Namespace) -> None:
    print(json.dumps(request("GET", "/pages/" + args.page_id), indent=2, ensure_ascii=False))


def cmd_create_page(args: argparse.Namespace) -> None:
    body: dict[str, Any] = {
        "parent": {"page_id": args.parent},
        "properties": {
            "title": {
                "title": [{"type": "text", "text": {"content": args.title}}],
            }
        },
    }
    print(json.dumps(request("POST", "/pages", body), indent=2, ensure_ascii=False))


def cmd_raw(args: argparse.Namespace) -> None:
    body = json.loads(args.body) if args.body else None
    print(json.dumps(request(args.method, args.path, body), indent=2, ensure_ascii=False))


def main() -> None:
    p = argparse.ArgumentParser(prog="notion_api.py")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("search")
    s.add_argument("query")
    s.add_argument("--limit", type=int, default=10)
    s.set_defaults(func=cmd_search)
    g = sub.add_parser("get-page")
    g.add_argument("page_id")
    g.set_defaults(func=cmd_get_page)
    c = sub.add_parser("create-page")
    c.add_argument("--parent", required=True)
    c.add_argument("--title", required=True)
    c.set_defaults(func=cmd_create_page)
    r = sub.add_parser("raw")
    r.add_argument("method")
    r.add_argument("path")
    r.add_argument("--body")
    r.set_defaults(func=cmd_raw)
    args = p.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
