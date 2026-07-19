# fetch.py <url> [selector] — rendered page text via anti-detect Firefox
import sys

from camoufox.sync_api import Camoufox

url = sys.argv[1] if len(sys.argv) > 1 else sys.exit("usage: fetch.py <url> [selector]")
sel = sys.argv[2] if len(sys.argv) > 2 else "body"
with Camoufox(headless=True, humanize=True) as browser:
    page = browser.new_page()
    page.goto(url, wait_until="domcontentloaded", timeout=60_000)
    page.wait_for_timeout(2_500)  # let JS challenges / hydration settle
    print(page.inner_text(sel)[:20_000])
