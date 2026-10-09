## What & why

<!-- One or two sentences. Reference the roadmap work package, e.g. "F5: typed handlers". -->

WP:

## Definition of Done

See `docs/contributing/documentation-guide.md` §2.

- [ ] Code complete; CI passes (lint, `test -race`, govulncheck)
- [ ] Tests cover the change, including failure paths
- [ ] Godoc for every new or changed exported identifier
- [ ] API change? Follows `docs/contributing/api-guidelines.md`; `api/*.txt` updated (`make api-update`), renames deprecated, not removed (or N/A)
- [ ] User docs added or updated (guide / concept / reference)
- [ ] Doc examples compile (from `examples/`) or are marked illustrative
- [ ] CHANGELOG entry under `Unreleased`
- [ ] Breaking change? Migration notes in CHANGELOG + upgrade guide (or N/A)
- [ ] Design impact? design.md decision log / ADR updated (or N/A)
- [ ] Roadmap impact? WP status or scope updated (or N/A)
