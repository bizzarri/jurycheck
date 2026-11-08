# Prototype validation

- 26 Go tests passed.
- `go vet ./...` passed.
- Go statement coverage: 85.9% (this is code coverage, not accessibility coverage).
- Linux executable run on both supplied CGJA template versions in draft mode.
- Original: 0 issue findings and 51 review candidates.
- Revised: 0 issue findings and 44 review candidates.
- Those counts include intentional template placeholders; neither result establishes accessibility.
- Windows amd64 console executable cross-compiled; PE32+ format verified; not run on Windows.
- Toolchain used for these prototype builds: Ubuntu Go 1.22.2. Rebuild using the court's approved current Go toolchain for deployment.
- HTML structure and local-only assets checked programmatically. HTML was not tested with a screen reader.
- No PDF validator or screen-reader testing is included in this version.
