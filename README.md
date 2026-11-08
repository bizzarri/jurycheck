# JuryCheck 0.1.0

An offline Go command-line tool for limited structural accessibility checks of
Word reports (`.docx`) and templates (`.dotx`). Built as a practical first stage
for civil grand jury report preparation. It has no third-party Go dependencies.

**This version does not test PDFs and does not certify ADA, WCAG or PDF/UA
conformance.** Manual review and separate testing of the final PDF are required
on every run, including a run reporting zero machine-detected issues.

## Windows quick start

Install a current supported Go release, clone or download this repository, and
open PowerShell in its folder:

```powershell
go test ./...
go build -trimpath -o jurycheck.exe .
.\jurycheck.exe -mode draft -html review.html -json review.json "C:\Reports\Draft Report.docx"
```

Open `review.html` in your browser. It is self-contained and uses no web assets.
Use `-mode publication` (the default) for a completed report. The tool does not
modify the source file and refuses to overwrite an existing output report.
Choose fresh output filenames for each run. Options go before the input path.

This source repository does not include compiled executables or report documents.
Court IT can review and build the source with its approved Go toolchain.

## Build from source

Go 1.22 or newer is sufficient. A current supported Go release is recommended
for deployment. There are no external modules to fetch.

```sh
go test ./...
go vet ./...
go build -buildvcs=false -trimpath -o jurycheck .
./jurycheck -mode publication -html review.html -json review.json report.docx
```

Cross-compile on Linux/macOS for Windows if desired:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -trimpath -o jurycheck.exe .
```

## Output and exit codes

- `issue`: a machine-detectable source condition, such as missing title metadata,
  missing language declaration or an unreplaced template placeholder in publication
  mode. This is an application rule finding, not a legal determination or a
  complete evaluation of a WCAG success criterion.
- `review`: something that needs interpretation, such as skipped heading levels,
  typed list-label candidates, table-header settings, drawing descriptions, or
  unsupported content. An intentional statutory label is not automatically wrong.
- No overall numerical accessibility score or conformance pass is produced.
- Exit `0`: processing succeeded, with no `issue` findings. Review items and
  mandatory manual checks may still remain.
- Exit `1`: processing succeeded and found one or more `issue` findings.
- Exit `2`: invalid invocation, unsupported input, parsing/processing error or output
  error. This includes a PDF input: PDF testing is explicitly not implemented.

Each JSON/HTML report includes the input SHA-256, tool version, time in UTC,
rule coverage, findings, scanned story parts, limitations and manual-review tasks.
A paragraph reference is its XML/story position, **not a printed page number**.
Templates intentionally retain placeholders; those are review items in draft
mode and issue findings in publication mode. Tests generate their own synthetic
Word fixtures; no jury report documents are included.

## Checks implemented

- Nonempty core document-title metadata.
- Paragraph/style/default language declarations (their correctness is manual).
- Effective paragraph outline levels, including inherited/custom heading styles;
  missing headings and skipped levels are review candidates.
- Numbering inherited through paragraph styles; unresolved numbering levels;
  manually typed F/R/number/bullet label candidates.
- First-row repeat-header markers, empty first-row cells, merged and nested tables.
- Modern Word drawing descriptions and Microsoft decorative markers.
- Empty/generic hyperlink text, with image-only links left for manual assessment.
- External non-hyperlink relationships, reported without fetching their targets.
- Known template-placeholder patterns.
- Text boxes, legacy VML, embedded objects, imported chunks, fields and tracked
  changes, which are flagged for review rather than silently assumed accessible.
- Main document, standard header/footer parts, footnotes and endnotes.

## Important limitations

The checker reads XML; it does not render Word pages, execute macros, access the
network, upload documents, run OCR, measure contrast or drive a screen reader.
It does not inspect whether an alt description is useful or truthful. A heading
may be correctly tagged yet wrongly named. A table can have a repeat-header
marker and still export incorrectly. Link destinations are not visited or tested.

It does not fully resolve conditional table styles, script-specific language attributes,
numbering style links/overrides, comments, charts' internal data, content controls,
application-specific extensions or fields. Tracked changes are not interpreted as
a final accepted document. Such limitations can cause review candidates or missed
issues. Do not interpret absence of findings as evidence of completeness.

No source text excerpts are included by default; `-include-text` explicitly adds
short excerpts to findings. Generated reports can still contain filenames and
structural details; handle them according to court policy.

Package protections: ZIP parts are not extracted, duplicate/unsafe part names
are rejected, total advertised uncompressed size is limited to 512 MiB, individual
XML parts to 32 MiB, XML nesting to 256 levels, and entry count to 10,000. These
limits reduce resource risks; they are not a security certification. Very large
legitimate files may need adjusted limits and review.

## Required publication process

1. Fix source issues and review every candidate in Word.
2. Review headings, lists, tables, descriptions, contrast, reading order and links
   manually. Proofread any OCR against the official source.
3. Export a tagged PDF with structure tags enabled.
4. Independently test that PDF, including its appended exhibits and final merge.
5. Record a trained reviewer's screen-reader/manual checks and acceptance.

## Next component: PDF testing

A future Go adapter should run a court-approved PDF validator locally and import
its structured results, rather than treat searches for `/StructTreeRoot` or
`/Lang` in raw PDF bytes as validation. PDF content streams, object streams,
parent trees, annotations and tags need proper parsing. Even a validated PDF
structure cannot establish meaningful descriptions or reading order by itself.
This package deliberately does not provide superficial PDF "passes."

## Primary references

- Microsoft Word document accessibility guidance:
  https://support.microsoft.com/en-us/accessibility/word/make-your-word-documents-accessible-to-people-with-disabilities
- Microsoft accessible PDF export guidance:
  https://support.microsoft.com/en-us/accessibility/office-accessibility/create-accessible-pdfs
- Microsoft WordprocessingML structure and styles:
  https://learn.microsoft.com/en-us/office/open-xml/word/structure-of-a-wordprocessingml-document
  https://learn.microsoft.com/en-us/office/open-xml/word/how-to-create-and-add-a-paragraph-style-to-a-word-processing-document
- W3C on selecting evaluation tools and limits of automation:
  https://www.w3.org/WAI/test-evaluate/tools/selecting/

## License

MIT; see LICENSE. This is an independent prototype, not a product or endorsement
of CGJA, Amador County, Microsoft, or any court.
