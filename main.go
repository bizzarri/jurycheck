// JuryCheck performs limited, offline structural checks of Word documents.
// It does not certify ADA, WCAG or PDF/UA conformance.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const version = "0.1.0"
const maxXML = 32 << 20
const maxPackage = 512 << 20
const wNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
const strictWNS = "http://purl.oclc.org/ooxml/wordprocessingml/main"

type node struct {
	Name  xml.Name
	Attrs []xml.Attr
	Text  string
	Kids  []*node
}

func (n *node) attr(k string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attrs {
		if a.Name.Local == k {
			return a.Value
		}
	}
	return ""
}
func (n *node) child(k string) *node {
	if n == nil {
		return nil
	}
	for _, c := range n.Kids {
		if c.Name.Local == k {
			return c
		}
	}
	return nil
}
func (n *node) at(keys ...string) *node {
	for _, k := range keys {
		n = n.child(k)
	}
	return n
}
func (n *node) val() string { return n.attr("val") }
func (n *node) word(k string) bool {
	return n != nil && n.Name.Local == k && (n.Name.Space == wNS || n.Name.Space == strictWNS)
}
func walk(n *node, fn func(*node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, c := range n.Kids {
		walk(c, fn)
	}
}
func descendants(n *node, k string) []*node {
	var out []*node
	walk(n, func(x *node) {
		if x.Name.Local == k {
			out = append(out, x)
		}
	})
	return out
}
func plain(n *node) string {
	var b strings.Builder
	walk(n, func(x *node) {
		if x.word("t") {
			b.WriteString(x.Text)
		}
		if x.word("tab") || x.word("br") {
			b.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}
func on(n *node) bool {
	if n == nil {
		return false
	}
	v := strings.ToLower(n.val())
	return v != "0" && v != "false" && v != "off"
}
func parseXML(r io.Reader) (*node, error) {
	d := xml.NewDecoder(r)
	var root *node
	var stack []*node
	for {
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch v := t.(type) {
		case xml.StartElement:
			if len(stack) >= 256 {
				return nil, errors.New("XML nesting exceeds 256 levels")
			}
			n := &node{Name: v.Name, Attrs: v.Attr}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.Kids = append(p.Kids, n)
			} else {
				if root != nil {
					return nil, errors.New("multiple XML roots")
				}
				root = n
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(v)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("unbalanced XML")
			}
			stack = stack[:len(stack)-1]
		}
	}
	if root == nil {
		return nil, errors.New("empty XML")
	}
	return root, nil
}

type pkg struct {
	files map[string]*zip.File
	z     *zip.ReadCloser
}

func openPackage(name string) (*pkg, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".docx" && ext != ".dotx" {
		return nil, fmt.Errorf("unsupported file type %q: only .docx and .dotx are checked; PDF is not implemented", ext)
	}
	z, e := zip.OpenReader(name)
	if e != nil {
		return nil, e
	}
	p := &pkg{files: map[string]*zip.File{}, z: z}
	var total uint64
	fail := func(e error) (*pkg, error) { z.Close(); return nil, e }
	if len(z.File) > 10000 {
		return fail(errors.New("too many package entries"))
	}
	for _, f := range z.File {
		if _, ok := p.files[f.Name]; ok {
			return fail(fmt.Errorf("duplicate package part %s", f.Name))
		}
		if strings.Contains(f.Name, "\\") || strings.HasPrefix(f.Name, "../") || f.Name == ".." || strings.HasPrefix(f.Name, "/") || path.Clean(f.Name) != strings.TrimSuffix(f.Name, "/") {
			return fail(errors.New("unsafe package part name"))
		}
		if f.UncompressedSize64 > maxPackage || total > maxPackage-f.UncompressedSize64 {
			return fail(errors.New("package exceeds 512 MiB uncompressed limit"))
		}
		total += f.UncompressedSize64
		p.files[f.Name] = f
	}
	if p.files["[Content_Types].xml"] == nil || p.files["word/document.xml"] == nil {
		return fail(errors.New("not a supported Word package"))
	}
	return p, nil
}
func (p *pkg) read(name string) (*node, error) {
	f := p.files[name]
	if f == nil {
		return nil, nil
	}
	if f.UncompressedSize64 > maxXML {
		return nil, fmt.Errorf("%s exceeds 32 MiB XML limit", name)
	}
	r, e := f.Open()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, maxXML+1))
	if e != nil {
		return nil, e
	}
	if len(b) > maxXML {
		return nil, errors.New("XML size limit exceeded")
	}
	n, e := parseXML(strings.NewReader(string(b)))
	if e != nil {
		return nil, fmt.Errorf("%s: %w", name, e)
	}
	return n, nil
}

type finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Location string `json:"location"`
	Message  string `json:"message"`
	Action   string `json:"action"`
	Evidence string `json:"evidence,omitempty"`
}
type check struct {
	Rule        string `json:"rule"`
	Description string `json:"description"`
	Result      string `json:"result"`
}
type report struct {
	Tool        string    `json:"tool"`
	Version     string    `json:"version"`
	File        string    `json:"file"`
	SHA256      string    `json:"sha256"`
	Generated   string    `json:"generated_utc"`
	Mode        string    `json:"mode"`
	Status      string    `json:"status"`
	Issues      int       `json:"issues"`
	Reviews     int       `json:"reviews"`
	Parts       []string  `json:"parts_scanned"`
	Findings    []finding `json:"findings"`
	Checks      []check   `json:"checks"`
	Manual      []string  `json:"manual_review_required"`
	Limitations []string  `json:"limitations"`
}

var manualTasks = []string{
	"Confirm that heading labels and hierarchy express the intended organization; inspect the Navigation Pane.",
	"Verify every meaningful image description and any chart, map or diagram data; check decorative classifications.",
	"Read each table with assistive technology and verify row/column relationships and blank-cell meaning.",
	"Check visual contrast, use of color, magnification/reflow, reading order, keyboard access and link destinations.",
	"Proofread OCR and verify names, dates, amounts, quotations, findings/recommendations and references.",
	"Export a tagged PDF, then test the actual public PDF independently, including appended and merged material.",
	"Record screen-reader testing and reviewer acceptance. These automated results are not a compliance determination.",
}
var limits = []string{
	"Word XML structure only. No Word rendering, PDF parsing, OCR, contrast measurement or screen-reader execution.",
	"Alt-text existence is checked; its accuracy, completeness and suitability cannot be established automatically.",
	"Paragraph locations are XML/story positions, not printed page numbers. Headers/footers and footnotes/endnotes are scanned separately.",
	"Legacy VML, text boxes, fields, tracked changes and embedded objects need manual review. Conditional table styles, theme properties and application-specific export behavior are not fully evaluated.",
	"A repeating header-row marker is a useful source signal, not proof of correct PDF table semantics. Language checks identify declarations, not whether they match the text.",
	"ZIP/XML inputs are read locally. No network requests, telemetry, document changes, macro execution or automatic fixes are performed.",
}
var checks = []check{
	{"metadata-title", "Nonempty document-title metadata", ""},
	{"language", "Default or paragraph/style language declarations", ""},
	{"headings", "Presence, effective outline levels and skipped heading levels", ""},
	{"numbering", "List definitions and manually typed list-label candidates", ""},
	{"table-header", "Header-row markers in Word tables", ""},
	{"table-complexity", "Merged, nested and blank-header cells", ""},
	{"image-description", "Drawing alternative text or decorative marking", ""},
	{"links", "Empty/generic hyperlink names and external-resource review", ""},
	{"placeholders", "Known template placeholders", ""},
	{"unsupported-content", "Text boxes, tracked changes, fields and embedded content", ""},
}

type checker struct {
	rep         report
	styles      map[string]*node
	defStyle    string
	defaults    *node
	nums        map[string]string
	levels      map[string]map[string]bool
	includeText bool
	observed    map[string]bool
}

func (c *checker) add(rule, sev, loc, msg, action, text string) {
	c.observed[rule] = true
	f := finding{Rule: rule, Severity: sev, Location: loc, Message: msg, Action: action}
	if c.includeText {
		rr := []rune(text)
		if len(rr) > 160 {
			text = string(rr[:160]) + "…"
		}
		f.Evidence = text
	}
	c.rep.Findings = append(c.rep.Findings, f)
}
func (c *checker) chain(id string) []*node {
	var out []*node
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		seen[id] = true
		s := c.styles[id]
		if s == nil {
			break
		}
		out = append(out, s)
		id = s.child("basedOn").val()
	}
	return out
}
func (c *checker) props(p *node, typ string) []*node {
	direct := p.child(typ)
	if typ == "rPr" {
		direct = p.at("pPr", "rPr")
	}
	out := []*node{direct}
	id := p.at("pPr", "pStyle").val()
	if id == "" {
		id = c.defStyle
	}
	for _, s := range c.chain(id) {
		out = append(out, s.child(typ))
	}
	return out
}
func firstVal(props []*node, keys ...string) string {
	for _, p := range props {
		if n := p.at(keys...); n != nil {
			return n.val()
		}
	}
	return ""
}
func (c *checker) heading(p *node) int {
	v := firstVal(c.props(p, "pPr"), "outlineLvl")
	if v == "" {
		return 0
	}
	i, e := strconv.Atoi(v)
	if e == nil && i >= 0 && i < 9 {
		return i + 1
	}
	return 0
}
func (c *checker) language(p *node) string {
	return firstVal(append(c.props(p, "rPr"), c.defaults.at("rPrDefault", "rPr")), "lang")
}

// languageDeclared also accepts explicit run/character-style declarations when
// no paragraph or document language is available. It does not infer language.
func (c *checker) languageDeclared(p *node) bool {
	if strings.TrimSpace(c.language(p)) != "" {
		return true
	}
	seen, good := false, true
	walk(p, func(r *node) {
		if !r.word("r") || plain(r) == "" {
			return
		}
		seen = true
		props := []*node{r.child("rPr")}
		for _, st := range c.chain(r.at("rPr", "rStyle").val()) {
			props = append(props, st.child("rPr"))
		}
		if strings.TrimSpace(firstVal(props, "lang")) == "" {
			good = false
		}
	})
	return seen && good
}

func (c *checker) num(p *node) (string, string) {
	props := c.props(p, "pPr")
	id := firstVal(props, "numPr", "numId")
	lvl := firstVal(props, "numPr", "ilvl")
	if lvl == "" {
		lvl = "0"
	}
	return id, lvl
}

var typedList = regexp.MustCompile(`^(?:[FR][0-9]+[.:]|[0-9]+[.)]|[•●▪])\s+`)
var placeholder = regexp.MustCompile(`(?i)\[(?:highlight[^\]]*|report title|value|column [0-9]+|insert optional[^\]]*|name of county|county|202x[^\]]*|public release date[^\]]*|major discussion[^\]]*|subtopic heading[^\]]*)\]`)

func (c *checker) scanPart(name string, n *node) {
	prev, paraIndex, tableIndex, imageIndex, linkIndex := 0, 0, 0, 0, 0
	walk(n, func(x *node) {
		switch {
		case x.word("p"):
			paraIndex++
			loc := fmt.Sprintf("%s paragraph %d", name, paraIndex)
			t := plain(x)
			h := c.heading(x)
			if h > 0 {
				if h > prev+1 {
					c.add("headings", "review", loc, fmt.Sprintf("Heading level jumps from %d to %d.", prev, h), "Confirm the hierarchy and apply the appropriate heading level.", t)
				}
				prev = h
			}
			if t != "" && !c.languageDeclared(x) {
				c.add("language", "issue", loc, "No language declaration was resolved for this text paragraph.", "Set the correct document and passage language in Word.", t)
			}
			id, lvl := c.num(x)
			if id != "" && id != "0" {
				a, ok := c.nums[id]
				if !ok || !c.levels[a][lvl] {
					c.add("numbering", "review", loc, "List numbering cannot be fully resolved to a defined level.", "Verify the list in Word; numbering style links or application-specific behavior may need manual inspection.", t)
				}
			} else if typedList.MatchString(t) {
				c.add("numbering", "review", loc, "Text resembles a manually typed list label.", "Determine whether it is a list; if so use Word list formatting. A statutory label may be intentional.", t)
			}
			if placeholder.MatchString(t) {
				sev := "review"
				if c.rep.Mode == "publication" {
					sev = "issue"
				}
				c.add("placeholders", sev, loc, "A known template-placeholder pattern remains.", "Replace or remove the placeholder before publication; check whether bracketed wording is intentional.", t)
			}
		case x.word("tbl"):
			tableIndex++
			loc := fmt.Sprintf("%s table %d", name, tableIndex)
			var rows []*node
			for _, y := range x.Kids {
				if y.word("tr") {
					rows = append(rows, y)
				}
			}
			if len(rows) == 0 {
				c.add("table-header", "review", loc, "Table has no directly represented rows.", "Review content controls and table structure in Word.", "")
				break
			}
			if !on(rows[0].at("trPr", "tblHeader")) {
				c.add("table-header", "review", loc, "First row is not marked as a repeating header.", "If this is a data table, identify the header row and verify the exported PDF header cells. Avoid layout tables.", "")
			}
			for _, cell := range rows[0].Kids {
				if cell.word("tc") && plain(cell) == "" {
					c.add("table-complexity", "review", loc, "An empty first-row cell may create an ambiguous header.", "Explain or label the column/row relationship and verify table navigation.", "")
					break
				}
			}
			complex := false
			walk(x, func(y *node) {
				if y != x && y.word("tbl") {
					complex = true
				}
				if y.word("vMerge") || y.word("hMerge") {
					complex = true
				}
				if y.word("gridSpan") {
					v, _ := strconv.Atoi(y.val())
					if v > 1 {
						complex = true
					}
				}
			})
			if complex {
				c.add("table-complexity", "review", loc, "Merged or nested table structure was detected.", "Simplify where possible; verify all header/data associations manually.", "")
			}
		case x.Name.Local == "docPr" && strings.Contains(x.Name.Space, "wordprocessingDrawing"):
			imageIndex++
			loc := fmt.Sprintf("%s drawing %d", name, imageIndex)
			dec := false
			walk(x, func(y *node) {
				if y.Name.Local == "decorative" && strings.Contains(y.Name.Space, "drawing/2017/decorative") {
					v := strings.ToLower(y.attr("val"))
					dec = v == "1" || v == "true"
				}
			})
			alt := strings.TrimSpace(x.attr("descr"))
			title := strings.TrimSpace(x.attr("title"))
			if !dec && alt == "" {
				msg := "Drawing has no description or decorative marker."
				if title != "" {
					msg = "Drawing has a title but no description; title-only support needs review."
				}
				c.add("image-description", "review", loc, msg, "Supply an appropriate description or mark a purely decorative drawing as decorative. Charts may need accessible data.", "")
			}
			if dec && alt != "" {
				c.add("image-description", "review", loc, "Decorative marking and a description are both present.", "Confirm whether the drawing conveys information and remove contradictory settings.", "")
			}
		case x.word("hyperlink"):
			linkIndex++
			loc := fmt.Sprintf("%s link %d", name, linkIndex)
			t := plain(x)
			low := strings.ToLower(strings.TrimSpace(t))
			if t == "" || low == "click here" || low == "here" || low == "read more" || low == "link" {
				c.add("links", "review", loc, "Hyperlink has an empty or generic text label.", "Check for an image-only link with an accessible name; otherwise provide descriptive link text.", t)
			}
		}
	})
	if name == "word/document.xml" && prev == 0 {
		c.add("headings", "review", name, "No outline-level headings were found.", "For a structured report, apply real heading styles. Short documents may not require headings.", "")
	}
	categories := map[string]string{"txbxContent": "Text boxes", "object": "Embedded objects", "altChunk": "Imported content chunks", "ins": "Tracked insertions", "del": "Tracked deletions", "fldSimple": "Simple fields", "instrText": "Complex fields", "imagedata": "Legacy VML images"}
	keys := make([]string, 0, len(categories))
	for k := range categories {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		count := len(descendants(n, k))
		if count > 0 {
			c.add("unsupported-content", "review", name, fmt.Sprintf("%s detected (%d).", categories[k], count), "Inspect this content and the final PDF manually; this checker does not fully evaluate it.", "")
		}
	}
}
func analyze(name, mode string, includeText bool) (report, error) {
	p, e := openPackage(name)
	if e != nil {
		return report{}, e
	}
	defer p.z.Close()
	f, e := os.Open(name)
	if e != nil {
		return report{}, e
	}
	hash := sha256.New()
	_, e = io.Copy(hash, f)
	f.Close()
	if e != nil {
		return report{}, e
	}
	c := &checker{styles: map[string]*node{}, nums: map[string]string{}, levels: map[string]map[string]bool{}, includeText: includeText, observed: map[string]bool{}}
	c.rep = report{Tool: "JuryCheck", Version: version, File: filepath.Base(name), SHA256: hex.EncodeToString(hash.Sum(nil)), Generated: time.Now().UTC().Format(time.RFC3339), Mode: mode, Status: "Manual review required — limited automated checks", Manual: manualTasks, Limitations: limits, Findings: []finding{}, Parts: []string{}}
	core, e := p.read("docProps/core.xml")
	if e != nil {
		return report{}, e
	}
	title := ""
	if core != nil {
		if n := core.child("title"); n != nil {
			title = strings.TrimSpace(n.Text)
		}
	}
	if title == "" {
		c.add("metadata-title", "issue", "docProps/core.xml", "Document-title metadata is empty or absent.", "Set a meaningful document title in File > Info > Properties.", "")
	}
	s, e := p.read("word/styles.xml")
	if e != nil {
		return report{}, e
	}
	if s != nil {
		c.defaults = s.child("docDefaults")
		for _, st := range s.Kids {
			if st.Name.Local == "style" {
				c.styles[st.attr("styleId")] = st
				if st.attr("type") == "paragraph" && (st.attr("default") == "1" || st.attr("default") == "true") {
					c.defStyle = st.attr("styleId")
				}
			}
		}
	}
	nu, e := p.read("word/numbering.xml")
	if e != nil {
		return report{}, e
	}
	if nu != nil {
		for _, n := range nu.Kids {
			if n.Name.Local == "num" {
				c.nums[n.attr("numId")] = n.child("abstractNumId").val()
			}
			if n.Name.Local == "abstractNum" {
				m := map[string]bool{}
				for _, l := range n.Kids {
					if l.Name.Local == "lvl" {
						m[l.attr("ilvl")] = true
					}
				}
				c.levels[n.attr("abstractNumId")] = m
			}
		}
	}
	// Only known Word story parts are evaluated, in deterministic order.
	names := []string{}
	for name := range p.files {
		base := path.Base(name)
		if name == "word/document.xml" || name == "word/footnotes.xml" || name == "word/endnotes.xml" || (path.Dir(name) == "word" && (strings.HasPrefix(base, "header") || strings.HasPrefix(base, "footer")) && strings.HasSuffix(base, ".xml")) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		n, e := p.read(name)
		if e != nil {
			return report{}, e
		}
		if name == "word/document.xml" && !n.word("document") {
			return report{}, errors.New("word/document.xml has an unexpected root element")
		}
		c.rep.Parts = append(c.rep.Parts, name)
		c.scanPart(name, n)
	}
	// External relationships are reported without fetching their targets.
	relnames := []string{}
	for n := range p.files {
		if strings.HasSuffix(n, ".rels") {
			relnames = append(relnames, n)
		}
	}
	sort.Strings(relnames)
	for _, name := range relnames {
		rr, e := p.read(name)
		if e != nil {
			return report{}, e
		}
		for _, rel := range rr.Kids {
			if rel.attr("TargetMode") == "External" && !strings.HasSuffix(rel.attr("Type"), "/hyperlink") {
				c.add("links", "review", name+" relationship "+rel.attr("Id"), "An external resource relationship is present.", "Verify that all essential content is available offline and that linked resources are accessible. Nothing was fetched.", "")
			}
		}
	}
	for _, f := range c.rep.Findings {
		if f.Severity == "issue" {
			c.rep.Issues++
		} else {
			c.rep.Reviews++
		}
	}
	for _, x := range checks {
		if c.observed[x.Rule] {
			x.Result = "Findings listed; human interpretation required"
		} else {
			x.Result = "No finding from this limited rule; not a conformance pass"
		}
		c.rep.Checks = append(c.rep.Checks, x)
	}
	return c.rep, nil
}

const htmlReport = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>JuryCheck review for {{.File}}</title><style>body{font:1rem/1.6 system-ui,sans-serif;color:#17252e;background:#fff;margin:0}main{max-width:78ch;margin:auto;padding:1.4rem}h1,h2,h3{line-height:1.25;color:#173e53}a{color:#064f84}a:focus{outline:3px solid #a43800}section{border-top:1px solid #9cabaf;margin-top:1.5rem;padding-top:.6rem}dt{font-weight:700}dd{margin:0 0 .7rem}li{margin:.5rem 0}.wrap{overflow-x:auto}table{border-collapse:collapse;width:100%}th,td{border:1px solid #84939a;padding:.5rem;text-align:left;vertical-align:top}th{background:#edf2f5}code{overflow-wrap:anywhere}blockquote{margin:1rem 0;padding-left:1rem;border-left:3px solid #8b5a1c}button{font:inherit} @media print{main{max-width:none}section{break-inside:avoid}}</style></head><body><a href="#main">Skip to report</a><main id="main"><h1>JuryCheck document review</h1><p><strong>{{.Status}}</strong></p><p>{{.Issues}} machine-detected issue(s); {{.Reviews}} item(s) to review. Zero issues does not mean the document is accessible or legally compliant.</p><dl><dt>Source</dt><dd>{{.File}}</dd><dt>Tool and mode</dt><dd>{{.Tool}} {{.Version}} · {{.Mode}}</dd><dt>Generated UTC</dt><dd>{{.Generated}}</dd><dt>SHA-256</dt><dd><code>{{.SHA256}}</code></dd></dl><h2>Findings</h2>{{range .Findings}}<section><h3>{{.Rule}} — {{.Severity}}</h3><p><strong>Location:</strong> {{.Location}}</p><p>{{.Message}}</p><p><strong>Action:</strong> {{.Action}}</p>{{if .Evidence}}<blockquote>{{.Evidence}}</blockquote>{{end}}</section>{{else}}<p>No findings from the implemented rules. Continue with every manual check below.</p>{{end}}<h2>Manual review required</h2><ol>{{range .Manual}}<li>{{.}}</li>{{end}}</ol><h2>Rule coverage</h2><div class="wrap"><table><caption>Limited structural rules implemented by this version</caption><thead><tr><th scope="col">Rule</th><th scope="col">Check</th><th scope="col">Result</th></tr></thead><tbody>{{range .Checks}}<tr><th scope="row">{{.Rule}}</th><td>{{.Description}}</td><td>{{.Result}}</td></tr>{{end}}</tbody></table></div><h2>Scope and limitations</h2><ul>{{range .Limitations}}<li>{{.}}</li>{{end}}</ul><h2>Scanned Word story parts</h2><ul>{{range .Parts}}<li><code>{{.}}</code></li>{{end}}</ul><p>Keep this report with the source version and the separately tested final PDF. A trained reviewer must decide whether the document is fit for publication.</p></main></body></html>`

func writeExclusive(name string, write func(io.Writer) error) error {
	f, e := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	if e = write(f); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	ok = true
	return nil
}
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("jurycheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ht := fs.String("html", "", "new HTML report path (will not overwrite)")
	js := fs.String("json", "", "new JSON report path (will not overwrite)")
	mode := fs.String("mode", "publication", "publication or draft")
	inc := fs.Bool("include-text", false, "include short source excerpts in reports (default off)")
	v := fs.Bool("version", false, "show version")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: jurycheck [options] report.docx\nOffline Word structural checks only; manual review is always required.")
		fs.PrintDefaults()
	}
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if *v {
		fmt.Fprintln(stdout, "JuryCheck", version)
		return 0
	}
	if fs.NArg() != 1 || (*mode != "draft" && *mode != "publication") {
		fs.Usage()
		return 2
	}
	input := fs.Arg(0)
	seen := map[string]bool{}
	for _, s := range []string{*ht, *js} {
		if s == "" {
			continue
		}
		abs, e := filepath.Abs(s)
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
		in, _ := filepath.Abs(input)
		if abs == in || seen[abs] {
			fmt.Fprintln(stderr, "report paths must differ from input and each other")
			return 2
		}
		seen[abs] = true
		if _, e = os.Stat(s); !os.IsNotExist(e) {
			fmt.Fprintln(stderr, "output already exists or is inaccessible:", s)
			return 2
		}
	}
	rep, e := analyze(input, *mode, *inc)
	if e != nil {
		fmt.Fprintln(stderr, "JuryCheck:", e)
		return 2
	}
	if *ht != "" {
		e = writeExclusive(*ht, func(w io.Writer) error {
			return template.Must(template.New("report").Parse(htmlReport)).Execute(w, rep)
		})
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
	}
	if *js != "" {
		e = writeExclusive(*js, func(w io.Writer) error { enc := json.NewEncoder(w); enc.SetIndent("", "  "); return enc.Encode(rep) })
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 2
		}
	}
	fmt.Fprintf(stdout, "JuryCheck %s: %s\n%d machine-detected issue(s); %d review item(s).\nManual review required. This is not an ADA/WCAG/PDF-UA certification.\n", version, rep.File, rep.Issues, rep.Reviews)
	if *ht == "" && *js == "" {
		for _, f := range rep.Findings {
			fmt.Fprintf(stdout, "[%s] %s | %s\n  %s\n  Action: %s\n", f.Severity, f.Rule, f.Location, f.Message, f.Action)
		}
	}
	if rep.Issues > 0 {
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
