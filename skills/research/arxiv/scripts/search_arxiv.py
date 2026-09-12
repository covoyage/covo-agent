#!/usr/bin/env python3
"""Search arXiv via the public Atom API. Stdlib only — no pip install.

Usage:
  search_arxiv.py search "attention mechanism" [--max 10] [--sort relevance|lastUpdatedDate|submittedDate]
  search_arxiv.py id 1706.03762
  search_arxiv.py search 'ti:"reinforcement learning" AND au:silver'
"""
from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

API = "http://export.arxiv.org/api/query"
ATOM = "{http://www.w3.org/2005/Atom}"
ARXIV = "{http://arxiv.org/schemas/atom}"


def fetch(url: str) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "covo-agent-arxiv/1.0"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.read()
    except urllib.error.URLError as e:
        sys.stderr.write(f"arxiv request failed: {e}\n")
        sys.exit(1)


def parse_entries(xml_bytes: bytes) -> list[dict]:
    root = ET.fromstring(xml_bytes)
    papers = []
    for entry in root.findall(f"{ATOM}entry"):
        authors = [a.findtext(f"{ATOM}name") or "" for a in entry.findall(f"{ATOM}author")]
        links = {lnk.get("title") or lnk.get("rel"): lnk.get("href") for lnk in entry.findall(f"{ATOM}link")}
        cats = [c.get("term") for c in entry.findall(f"{ATOM}category") if c.get("term")]
        papers.append({
            "id": (entry.findtext(f"{ATOM}id") or "").rsplit("/", 1)[-1],
            "title": " ".join((entry.findtext(f"{ATOM}title") or "").split()),
            "authors": authors,
            "published": (entry.findtext(f"{ATOM}published") or "")[:10],
            "updated": (entry.findtext(f"{ATOM}updated") or "")[:10],
            "summary": " ".join((entry.findtext(f"{ATOM}summary") or "").split()),
            "url": entry.findtext(f"{ATOM}id"),
            "pdf": links.get("pdf") or "",
            "categories": cats,
            "comment": entry.findtext(f"{ARXIV}comment") or "",
        })
    return papers


def cmd_search(args: argparse.Namespace) -> None:
    params = {
        "search_query": args.query,
        "start": "0",
        "max_results": str(args.max),
        "sortBy": args.sort,
        "sortOrder": "descending",
    }
    url = API + "?" + urllib.parse.urlencode(params)
    papers = parse_entries(fetch(url))
    print(json.dumps(papers, indent=2, ensure_ascii=False))


def cmd_id(args: argparse.Namespace) -> None:
    params = {"id_list": args.paper_id, "max_results": "1"}
    url = API + "?" + urllib.parse.urlencode(params)
    papers = parse_entries(fetch(url))
    if not papers:
        sys.stderr.write(f"paper not found: {args.paper_id}\n")
        sys.exit(1)
    print(json.dumps(papers[0], indent=2, ensure_ascii=False))


def main() -> None:
    p = argparse.ArgumentParser(prog="search_arxiv.py")
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("search")
    s.add_argument("query")
    s.add_argument("--max", type=int, default=10)
    s.add_argument("--sort", default="relevance", choices=["relevance", "lastUpdatedDate", "submittedDate"])
    s.set_defaults(func=cmd_search)
    i = sub.add_parser("id")
    i.add_argument("paper_id")
    i.set_defaults(func=cmd_id)
    args = p.parse_args()
    args.func(args)


if __name__ == "__main__":
    main()
