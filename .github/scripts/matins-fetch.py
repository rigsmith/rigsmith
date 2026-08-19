#!/usr/bin/env python3
"""Fetch Claude Code daily briefs from matins.news into one plain-text file.

Deterministic half of the matins-watch agentic workflow: the agent never talks to
the network, it only reads what this wrote. Fails loudly rather than handing the
agent an empty file — a silent empty brief reads as "nothing changed today".

matins.news serves 403 to default agent user-agents, so a UA is mandatory. Its
robots.txt allows the wildcard agent (it disallows ClaudeBot/GPTBot by name), so
this identifies itself as the repo it runs for and sleeps between requests.
"""

import argparse
import datetime as dt
import html
import re
import sys
import time
import urllib.error
import urllib.request

BASE = "https://matins.news"
UA = "rigsmith-matins-watch/1.0 (+https://github.com/rigsmith/rigsmith)"
CARD = re.compile(
    r'<a href="/daily/(\d{4}-\d{2}-\d{2})"[^>]*class="brief-card"[^>]*>\s*'
    r'<div class="brief-card-date">(.*?)</div>\s*'
    r'<div class="brief-card-excerpt">(\d+) changes?',   # "1 change" is singular
    re.S,
)


def get(url: str) -> str:
    req = urllib.request.Request(url, headers={"User-Agent": UA, "Accept": "text/html"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return r.read().decode("utf-8", "replace")


def strip_html(page: str) -> str:
    page = re.sub(r"<script.*?</script>", "", page, flags=re.S)
    page = re.sub(r"<style.*?</style>", "", page, flags=re.S)
    text = html.unescape(re.sub(r"<[^>]+>", "\n", page))
    lines = [ln.strip() for ln in text.split("\n")]
    lines = [ln for ln in lines if ln]
    # Drop the chrome: everything up to the "back" link, and the footer.
    for i, ln in enumerate(lines[:20]):
        if ln.startswith("←"):
            lines = lines[i + 1 :]
            break
    for i in range(len(lines) - 1, max(len(lines) - 6, 0) - 1, -1):
        if lines[i] == "Discord":
            lines = lines[:i]
            break
    return "\n".join(lines)


def plan(days, max_briefs: int) -> None:
    """Print the inclusive date windows a backfill needs, one per line, sized by BRIEF count.

    gh-aw refuses to let a workflow dispatch itself, so a large backfill is one dispatch per
    chunk rather than a self-chaining run. matins-backfill.sh feeds these straight to
    `gh workflow run`.
    """
    chunk_start, count = None, 0
    for date, _headline, changes in days:
        if chunk_start is None:
            chunk_start = date
        if changes:
            count += 1
        if count >= max_briefs:
            print(f"{chunk_start} {date}")
            chunk_start, count = None, 0
    if chunk_start is not None and count:
        print(f"{chunk_start} {days[-1][0]}")


def briefs(args) -> int:
    index = get(f"{BASE}/daily/")
    cards = CARD.findall(index)
    if not cards:
        sys.exit("matins.news archive index parsed to zero briefs — page shape changed")

    # Every linked day must have parsed into a card. A card whose markup does not match is
    # a day dropped from the window with nothing to show for it — which is how the singular
    # "1 change" wording silently hid 7 days before this check existed.
    linked = set(re.findall(r'href="/daily/(\d{4}-\d{2}-\d{2})"', index))
    unparsed = sorted(linked - {c[0] for c in cards})
    if unparsed:
        sys.exit(f"archive index links {len(unparsed)} briefs whose card did not parse: {unparsed}")

    days = sorted(
        (d, hd.strip(), int(n))
        for d, hd, n in cards
        if (not args.since or d >= args.since) and (not args.until or d <= args.until)
    )
    print(f"{len(cards)} briefs in archive, {len(days)} in window", file=sys.stderr)

    if args.plan:
        plan(days, args.max_briefs)
        return 0

    # Chunk on BRIEFS FETCHED, not calendar days: quiet days cost nothing, so a
    # day-count cap makes the token bill swing by 3x depending on where the window
    # lands. Every non-zero brief is ~7KB of text.
    out, fetched, next_since = [], 0, ""
    for date, headline, changes in days:
        if fetched >= args.max_briefs:
            next_since = date
            print(f"NOTE: capped at {args.max_briefs} briefs; next chunk starts {date}", file=sys.stderr)
            break
        if changes == 0:
            print(f"skip {date} (0 changes)", file=sys.stderr)
            continue
        if fetched:
            time.sleep(1.0)  # be a polite guest
        page = get(f"{BASE}/daily/{date}/")
        body = strip_html(page)
        if "changes /" not in body and "change /" not in body:
            sys.exit(f"day page {date} has no 'N changes /' header — page shape changed")
        out += [f"===== BRIEF {date} — {headline} =====", "", body, ""]
        fetched += 1
        print(f"fetched {date} ({changes} changes)", file=sys.stderr)

    covered = f"{days[0][0]} .. {days[-1][0]}" if days else "empty"
    header = ["# matins.news — Claude Code daily briefs", f"# window: {covered}"]
    if next_since:
        header[1] = f"# window: {days[0][0]} .. {next_since} (exclusive)"
        header.append(f"# BACKFILL-CONTINUES-FROM: {next_since}")
    header.append("")
    if not fetched:
        out.append("NO-NEW-BRIEFS: nothing in this window had any changes.")

    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write("\n".join(header + out))
    print(f"wrote {args.out} ({fetched} briefs)", file=sys.stderr)
    return fetched


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--since", default="", help="oldest brief date to include (YYYY-MM-DD)")
    p.add_argument("--until", default="", help="newest brief date to include (YYYY-MM-DD)")
    p.add_argument("--max-briefs", type=int, default=14, help="cap on briefs fetched per run")
    p.add_argument("--plan", action="store_true", help="print the chunk windows a backfill needs, then exit")
    p.add_argument("--out", default="matins-briefs.txt")
    args = p.parse_args()
    for field in ("since", "until"):
        v = getattr(args, field)
        if v:
            dt.date.fromisoformat(v)  # raises on garbage rather than silently matching nothing
    try:
        briefs(args)
    except urllib.error.HTTPError as e:
        sys.exit(f"matins.news returned HTTP {e.code} — check the user-agent gate")
    except urllib.error.URLError as e:
        sys.exit(f"could not reach matins.news: {e.reason}")


if __name__ == "__main__":
    main()
