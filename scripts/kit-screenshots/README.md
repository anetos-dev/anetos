# Kit screenshots

The screenshots of the design kits in the docs
(`docs/site/images/kits/*.webp`, shown by `docs/site/guides/styling.md`
and the kit guides) come from `run.sh`. Run it after changing a kit's
templates or updating its framework, and commit the images it writes:

```sh
scripts/kit-screenshots/run.sh              # every kit
scripts/kit-screenshots/run.sh pico bulma   # some of them
```

For each kit it builds the CLI from this checkout, makes a project
(`anetos new shop --css=<kit> --replace=<checkout>`, then `make:crud
Product …` and `make:auth`), serves it on a free local port (8180 and up),
registers a user, adds the same four products, and shoots:

| Image | What |
|---|---|
| `<kit>-list-light.webp`, `-dark` | The products list, 1100 px wide |
| `<kit>-form-light.webp`, `-dark` | The new-product form after an empty post, with its errors |
| `<kit>-menu.webp` | Bootstrap and Bulma: the header's menu open at a phone's width |

The `none` kit has no styles, so it gets the light images only.

It needs Go, curl, and Python 3 with
[Playwright](https://playwright.dev/python/) and its Chromium, and
[Pillow](https://pillow.readthedocs.io/) for the WebP files. In a
virtual environment (recent macOS and Linux refuse `pip install`
outside one):

```sh
python3 -m venv /tmp/shots && . /tmp/shots/bin/activate
pip install playwright pillow && playwright install chromium
scripts/kit-screenshots/run.sh
```

The Tailwind kit's pages use the stylesheet the kit carries, so Tailwind
CSS isn't needed. A port something else answers on is skipped, and an
app that doesn't start stops the script with its log.
