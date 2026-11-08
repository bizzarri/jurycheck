package main

import (
	"archive/zip"
	"bytes"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ns = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:adec="http://schemas.microsoft.com/office/drawing/2017/decorative"`
const baseStyles = `<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:lang w:val="en-US"/></w:rPr></w:rPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"/><w:style w:type="paragraph" w:styleId="Heading1"><w:pPr><w:outlineLvl w:val="0"/></w:pPr></w:style></w:styles>`

func fixture(t *testing.T, body, styles, numbering, core string) string {
	t.Helper()
	parts := map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<w:document " + ns + "><w:body>" + body + "</w:body></w:document>", "word/styles.xml": styles, "docProps/core.xml": core}
	if numbering != "" {
		parts["word/numbering.xml"] = numbering
	}
	return zipFixture(t, parts)
}
func zipFixture(t *testing.T, parts map[string]string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "test.docx")
	f, e := os.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	for n, v := range parts {
		s, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Write([]byte(v)); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return name
}

const title = `<core><title>A report</title></core>`

func get(t *testing.T, b, s, n, c string) report {
	t.Helper()
	r, e := analyze(fixture(t, b, s, n, c), "publication", false)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func has(r report, rule string) bool {
	for _, f := range r.Findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}
func TestInheritedOutlineHeading(t *testing.T) {
	s := strings.Replace(baseStyles, "</w:styles>", `<w:style w:styleId="Custom"><w:basedOn w:val="Heading1"/></w:style></w:styles>`, 1)
	r := get(t, `<w:p><w:pPr><w:pStyle w:val="Custom"/></w:pPr><w:r><w:t>Summary</w:t></w:r></w:p>`, s, "", title)
	if has(r, "headings") {
		t.Fatal(r.Findings)
	}
}
func TestSkippedHeading(t *testing.T) {
	r := get(t, `<w:p><w:pPr><w:outlineLvl w:val="2"/></w:pPr><w:r><w:t>Topic</w:t></w:r></w:p>`, baseStyles, "", title)
	if !has(r, "headings") {
		t.Fatal("missing skip")
	}
}
func TestOutlineNineSuppressesHeading(t *testing.T) {
	r := get(t, `<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:outlineLvl w:val="9"/></w:pPr><w:r><w:t>Body</w:t></w:r></w:p>`, baseStyles, "", title)
	if !has(r, "headings") {
		t.Fatal("should report no headings")
	}
}
func TestInheritedNumbering(t *testing.T) {
	s := strings.Replace(baseStyles, "</w:styles>", `<w:style w:styleId="Finding"><w:pPr><w:numPr><w:numId w:val="8"/></w:numPr></w:pPr></w:style></w:styles>`, 1)
	n := `<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:abstractNum w:abstractNumId="7"><w:lvl w:ilvl="0"/></w:abstractNum><w:num w:numId="8"><w:abstractNumId w:val="7"/></w:num></w:numbering>`
	r := get(t, `<w:p><w:pPr><w:pStyle w:val="Finding"/></w:pPr><w:r><w:t>A finding</w:t></w:r></w:p>`, s, n, title)
	if has(r, "numbering") {
		t.Fatal(r.Findings)
	}
}
func TestMissingNumberingLevel(t *testing.T) {
	r := get(t, `<w:p><w:pPr><w:numPr><w:numId w:val="88"/></w:numPr></w:pPr><w:r><w:t>A finding</w:t></w:r></w:p>`, baseStyles, "", title)
	if !has(r, "numbering") {
		t.Fatal("missing unresolved list")
	}
}
func TestTypedNumberIsReview(t *testing.T) {
	r := get(t, `<w:p><w:r><w:t>F1. A finding</w:t></w:r></w:p>`, baseStyles, "", title)
	for _, f := range r.Findings {
		if f.Rule == "numbering" {
			if f.Severity != "review" {
				t.Fatal(f)
			}
			return
		}
	}
	t.Fatal("missing typed candidate")
}
func TestEmptyCoreTitleNoPanic(t *testing.T) {
	r := get(t, `<w:p><w:r><w:t>Hello</w:t></w:r></w:p>`, baseStyles, "", "<core/>")
	if !has(r, "metadata-title") {
		t.Fatal("missing metadata finding")
	}
}
func TestMissingLanguage(t *testing.T) {
	s := strings.Replace(baseStyles, `<w:lang w:val="en-US"/>`, "", 1)
	r := get(t, `<w:p><w:r><w:t>Hello</w:t></w:r></w:p>`, s, "", title)
	if !has(r, "language") {
		t.Fatal("missing language finding")
	}
}
func TestDecorativeMarker(t *testing.T) {
	for _, v := range []string{"true", "false"} {
		r := get(t, `<w:p><w:r><w:drawing><wp:docPr id="1"><wp:extLst><adec:decorative val="`+v+`"/></wp:extLst></wp:docPr></w:drawing></w:r></w:p>`, baseStyles, "", title)
		if has(r, "image-description") != (v == "false") {
			t.Fatalf("dec=%s findings=%v", v, r.Findings)
		}
	}
}
func TestDescriptionDoesNotCertify(t *testing.T) {
	r := get(t, `<w:p><w:r><w:drawing><wp:docPr id="1" descr="Photo"/></w:drawing></w:r></w:p>`, baseStyles, "", title)
	if has(r, "image-description") {
		t.Fatal(r.Findings)
	}
	if !strings.Contains(r.Status, "Manual review") || len(r.Manual) == 0 {
		t.Fatal("must preserve manual obligations")
	}
}
func TestTableMarkerFalse(t *testing.T) {
	r := get(t, `<w:tbl><w:tr><w:trPr><w:tblHeader w:val="false"/></w:trPr><w:tc><w:p><w:r><w:t>Header</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`, baseStyles, "", title)
	if !has(r, "table-header") {
		t.Fatal("false marker treated as true")
	}
}
func TestSimpleHeaderTable(t *testing.T) {
	r := get(t, `<w:tbl><w:tr><w:trPr><w:tblHeader/></w:trPr><w:tc><w:p><w:r><w:t>Header</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`, baseStyles, "", title)
	if has(r, "table-header") {
		t.Fatal(r.Findings)
	}
}
func TestMergedTable(t *testing.T) {
	r := get(t, `<w:tbl><w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p/></w:tc></w:tr></w:tbl>`, baseStyles, "", title)
	if !has(r, "table-complexity") {
		t.Fatal("merged not detected")
	}
}
func TestGenericLink(t *testing.T) {
	r := get(t, `<w:p><w:hyperlink><w:r><w:t>click here</w:t></w:r></w:hyperlink></w:p>`, baseStyles, "", title)
	if !has(r, "links") {
		t.Fatal("generic link missed")
	}
}
func TestDraftPlaceholderSeverity(t *testing.T) {
	f := fixture(t, `<w:p><w:r><w:t>[REPORT TITLE]</w:t></w:r></w:p>`, baseStyles, "", title)
	for _, mode := range []string{"draft", "publication"} {
		r, e := analyze(f, mode, false)
		if e != nil {
			t.Fatal(e)
		}
		seen := false
		for _, x := range r.Findings {
			if x.Rule == "placeholders" {
				seen = true
				if (x.Severity == "issue") != (mode == "publication") {
					t.Fatal(x)
				}
			}
		}
		if !seen {
			t.Fatal("placeholder missed")
		}
	}
}
func TestMalformedXML(t *testing.T) {
	f := zipFixture(t, map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<bad>"})
	if _, e := analyze(f, "draft", false); e == nil {
		t.Fatal("accepted malformed XML")
	}
}
func TestZipTraversal(t *testing.T) {
	f := zipFixture(t, map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<document/>", "../bad": "x"})
	if _, e := openPackage(f); e == nil {
		t.Fatal("accepted traversal")
	}
}
func TestZipDuplicate(t *testing.T) {
	f, e := os.Create(filepath.Join(t.TempDir(), "dup.docx"))
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(f)
	for i := 0; i < 2; i++ {
		x, e := w.Create("word/document.xml")
		if e != nil {
			t.Fatal(e)
		}
		x.Write([]byte("x"))
	}
	w.Close()
	f.Close()
	if _, e := openPackage(f.Name()); e == nil {
		t.Fatal("accepted duplicate")
	}
}
func TestXMLDepthLimit(t *testing.T) {
	if _, e := parseXML(strings.NewReader(strings.Repeat("<x>", 300) + strings.Repeat("</x>", 300))); e == nil {
		t.Fatal("accepted excessive depth")
	}
}
func TestPDFRefused(t *testing.T) {
	if _, e := analyze("report.pdf", "publication", false); e == nil || !strings.Contains(e.Error(), "PDF is not implemented") {
		t.Fatal(e)
	}
}
func TestEvidenceOptIn(t *testing.T) {
	f := fixture(t, `<w:p><w:r><w:t>F1. SENSITIVE</w:t></w:r></w:p>`, baseStyles, "", title)
	r, e := analyze(f, "publication", false)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range r.Findings {
		if x.Evidence != "" {
			t.Fatal("source excerpt leaked by default")
		}
	}
}
func TestHTMLEscaping(t *testing.T) {
	r := report{File: "<script>alert(1)</script>", Findings: []finding{{Message: "<img src=x onerror=alert(1)>"}}}
	var b bytes.Buffer
	if e := template.Must(template.New("r").Parse(htmlReport)).Execute(&b, r); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(b.String(), "<script>") || strings.Contains(b.String(), "<img src=x") {
		t.Fatal("unescaped HTML")
	}
}
func TestNoOverwrite(t *testing.T) {
	f := filepath.Join(t.TempDir(), "out")
	os.WriteFile(f, []byte("keep"), 0600)
	e := writeExclusive(f, func(w io.Writer) error { return nil })
	if e == nil {
		t.Fatal("overwrote existing report")
	}
	b, _ := os.ReadFile(f)
	if string(b) != "keep" {
		t.Fatal("modified existing report")
	}
}

func TestRunLanguageWithoutDefaults(t *testing.T) {
	s := strings.Replace(baseStyles, `<w:lang w:val="en-US"/>`, "", 1)
	r := get(t, `<w:p><w:r><w:rPr><w:lang w:val="en-US"/></w:rPr><w:t>Declared language</w:t></w:r></w:p>`, s, "", title)
	if has(r, "language") {
		t.Fatal("ignored an explicit text-run language")
	}
}
func TestInheritedCharacterStyleLanguage(t *testing.T) {
	s := strings.Replace(baseStyles, `<w:lang w:val="en-US"/>`, "", 1)
	s = strings.Replace(s, `</w:styles>`, `<w:style w:type="character" w:styleId="English"><w:rPr><w:lang w:val="en-US"/></w:rPr></w:style></w:styles>`, 1)
	r := get(t, `<w:p><w:r><w:rPr><w:rStyle w:val="English"/></w:rPr><w:t>Declared language</w:t></w:r></w:p>`, s, "", title)
	if has(r, "language") {
		t.Fatal("ignored an inherited text-run language")
	}
}

func TestCLIReportAndExitCodes(t *testing.T) {
	f := fixture(t, `<w:p><w:r><w:t>[REPORT TITLE]</w:t></w:r></w:p>`, baseStyles, "", title)
	var out, err bytes.Buffer
	dir := t.TempDir()
	h, j := filepath.Join(dir, "report.html"), filepath.Join(dir, "report.json")
	if code := run([]string{"-mode", "draft", "-html", h, "-json", j, f}, &out, &err); code != 0 {
		t.Fatalf("draft exit=%d %s", code, err.String())
	}
	for _, p := range []string{h, j} {
		b, e := os.ReadFile(p)
		if e != nil || len(b) == 0 {
			t.Fatal(p, e)
		}
	}
	if code := run([]string{f}, &out, &err); code != 1 {
		t.Fatalf("publication exit=%d", code)
	}
	if code := run([]string{"-html", h, f}, &out, &err); code != 2 {
		t.Fatalf("overwrite exit=%d", code)
	}
	if code := run([]string{"document.pdf"}, &out, &err); code != 2 {
		t.Fatalf("PDF exit=%d", code)
	}
}
