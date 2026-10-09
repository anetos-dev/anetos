# SPDX-License-Identifier: Apache-2.0
"""Screenshots of each design kit's pages for the docs (run.sh runs it).

    python3 shots.py <out dir> <kit>=<base URL>...

For each kit: registers a user, adds the same products, then shoots the
products list and the new-product form with its errors, in light and in
dark (light only for none, which has no styles), at 1100 px wide; for
the kits with a menu button, the open menu at a phone's width. The images are WebP (<kit>-list-light.webp…).
"""
import io
import sys

from PIL import Image
from playwright.sync_api import sync_playwright

PRODUCTS = [
    ("Desk lamp", "39.5", True, "Warm white, dimmable."),
    ("Notebook", "4.25", True, ""),
    ("Standing desk", "420", False, "Back in stock\nnext month."),
    ("Monitor arm", "89", True, ""),
]
MENU_KITS = {"bootstrap", "bulma"}
LIGHT_ONLY = {"none"}  # no styles: the browser's own, light or dark


def save(png, path):
    Image.open(io.BytesIO(png)).convert("RGB").save(path, "WEBP", quality=82, method=6)
    print("wrote", path)


def shoot(p, out, kit, base):
    browser = p.chromium.launch()
    ctx = browser.new_context(viewport={"width": 1100, "height": 760})
    page = ctx.new_page()
    page.goto(base + "/register")
    page.fill("#name", "Ada Lovelace")
    page.fill("#email", "ada@example.com")
    page.fill("#password", "correct horse battery")
    page.fill("#password_confirmation", "correct horse battery")
    page.click("main form button[type=submit]")
    for name, price, stock, notes in PRODUCTS:
        page.goto(base + "/products/new")
        page.fill("#name", name)
        page.fill("#price", price)
        if stock:
            page.check("#in_stock")
        page.fill("#notes", notes)
        page.click("main form button[type=submit]")
    state = ctx.storage_state()
    for scheme in ("light",) if kit in LIGHT_ONLY else ("light", "dark"):
        c = browser.new_context(viewport={"width": 1100, "height": 760}, color_scheme=scheme, storage_state=state)
        pg = c.new_page()
        pg.goto(base + "/products")
        pg.mouse.move(0, 700)  # no hover on the header
        save(pg.screenshot(full_page=True), f"{out}/{kit}-list-{scheme}.webp")
        pg.goto(base + "/products/new")
        pg.click("main form button[type=submit]")
        pg.mouse.move(0, 700)
        save(pg.screenshot(full_page=True), f"{out}/{kit}-form-{scheme}.webp")
        c.close()
    if kit in MENU_KITS:
        c = browser.new_context(viewport={"width": 390, "height": 640}, storage_state=state)
        pg = c.new_page()
        pg.goto(base + "/products")
        pg.click(".navbar-toggler, .navbar-burger")
        pg.wait_for_timeout(500)  # the menu's transition
        pg.evaluate("document.activeElement.blur()")  # no focus ring
        pg.wait_for_timeout(300)  # the ring's transition
        pg.mouse.move(380, 630)
        save(pg.screenshot(), f"{out}/{kit}-menu.webp")
        c.close()
    browser.close()


def main():
    out, targets = sys.argv[1], sys.argv[2:]
    with sync_playwright() as p:
        for t in targets:
            kit, base = t.split("=", 1)
            shoot(p, out, kit, base)


if __name__ == "__main__":
    main()
