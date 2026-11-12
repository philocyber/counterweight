package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/phpdave11/gofpdf"
)

// Static instances of the console brand fonts (SIL OFL 1.1; notices in fonts/).
// Instrument Sans is instanced from the variable font in web/src/lib/fonts/brand.
var (
	//go:embed fonts/InstrumentSans-Regular.ttf
	fontSans []byte
	//go:embed fonts/InstrumentSans-SemiBold.ttf
	fontSansBold []byte
	//go:embed fonts/GeistMono-Regular.ttf
	fontMono []byte
)

// PDFOptions controls the shareable PDF export.
type PDFOptions struct {
	// IncludeSecrets disables credential and local-path redaction. Redaction is
	// on by default because the PDF is the export most likely to leave the console.
	IncludeSecrets bool
	// Variant is "technical" (default: every finding in full, evidence appendix)
	// or "executive" (summary, priorities and findings table only).
	Variant string
}

// Executive reports whether the short management variant was requested.
func (o PDFOptions) Executive() bool { return o.Variant == "executive" }

type rgb struct{ r, g, b int }

var (
	cGround  = rgb{0xf5, 0xf3, 0xec}
	cSurface = rgb{0xfb, 0xfa, 0xf6}
	cSunk    = rgb{0xec, 0xeb, 0xe3}
	cInk     = rgb{0x24, 0x36, 0x3d}
	cInk2    = rgb{0x33, 0x49, 0x4f}
	cMuted   = rgb{0x5a, 0x6b, 0x70}
	cTeal    = rgb{0x15, 0x8a, 0x88}
	cHair    = rgb{0xe2, 0xdf, 0xd5}
	cWhite   = rgb{0xff, 0xff, 0xff}
	cWarnBg  = rgb{0xfb, 0xf0, 0xd9}
	cWarn    = rgb{0x7a, 0x4b, 0x00}
)

// Severity colours stay distinct from the brand teal.
var sevColors = map[string]rgb{
	"critical": {0xbe, 0x12, 0x3c},
	"high":     {0xdc, 0x26, 0x26},
	"medium":   {0xb4, 0x53, 0x09},
	"low":      {0x25, 0x63, 0xeb},
}

func severityColor(s string) rgb {
	if c, ok := sevColors[strings.ToLower(strings.TrimSpace(s))]; ok {
		return c
	}
	return cMuted
}

const (
	pageW        = 210.0
	pageH        = 297.0
	marginX      = 18.0
	marginTop    = 24.0
	marginBottom = 20.0
	contentW     = pageW - 2*marginX

	// Evidence blocks are capped in the PDF; full content stays in the
	// Markdown, ZIP and JSON exports.
	evidenceLineCap = 60
	evidenceCharCap = 5000
)

var reviewStatusOrder = []string{
	db.FindingPending, db.FindingInProgress, db.FindingConfirmed, db.FindingFixed, db.FindingResolved,
	db.FindingRiskAccepted, db.FindingFalsePositive, db.FindingDuplicate, db.FindingIgnored,
}

type pdfWriter struct {
	f         *gofpdf.Fpdf
	in        Input // Task report input; zero for findings exports.
	opt       PDFOptions
	exec      Execution
	findings  []*db.DBFinding
	footerRef string // Identifies the document in every footer, e.g. "Task 42".
	showTask  bool   // Name each finding's task; used when findings span tasks.
	section   string
	total     int // total pages, known on the second pass

	// Navigation. pages records where each section starts on this pass;
	// prevPages holds the first pass's record so the contents page can print
	// page numbers. links are the contents entries' internal link targets.
	pages      map[string]int
	prevPages  map[string]int
	links      map[string]int
	tocEntries []tocEntry
	// appendix collects raw evidence moved out of the finding pages.
	appendix []appendixItem
}

type tocEntry struct {
	key, title string
	level      int
}

type appendixItem struct {
	ref, body string
}

// renderTwice builds the document twice: the first pass counts pages and
// records where sections start, so footers print "Page X of Y" and the
// contents page prints real page numbers.
func renderTwice(build func(total int, pages map[string]int) *pdfWriter) ([]byte, error) {
	first := build(0, nil)
	if err := first.f.Error(); err != nil {
		return nil, err
	}
	final := build(first.f.PageNo(), first.pages)
	var buf bytes.Buffer
	if err := final.f.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// PDF renders the task report as a branded, paginated document.
func PDF(in Input, opt PDFOptions) ([]byte, error) {
	kind := "Penetration test report"
	if in.CodeReview != nil {
		kind = "Security code review report"
	}
	if opt.Executive() {
		kind += " (executive summary)"
	}
	return renderTwice(func(total int, prev map[string]int) *pdfWriter {
		w := newPDFWriter(opt, total, reportFindings(in), kind+" - "+nz(in.Title, "Untitled task"),
			in.DocumentID(), in.Version, in.GeneratedAt)
		w.in = in
		w.prevPages = prev
		w.exec = DescribeExecution(in.Status, len(w.findings))
		w.collectAppendix()
		w.tocEntries = w.reportContents()
		w.taskCover()
		w.contents()
		w.summary()
		if in.CodeReview != nil {
			w.reviewTarget(in.CodeReview)
			if in.Lifecycle != nil {
				w.lifecycle()
			}
		} else {
			w.scope()
		}
		w.findingsSection("Registered findings ordered by severity, then newest first. Review status is shown per finding.",
			"No findings are registered for this task. This does not establish that the task completed or that the assets are safe.")
		if !opt.Executive() {
			w.scannerSections()
			w.authzSection()
		}
		w.appendixSection()
		w.closing()
		return w
	})
}

// reportContents lists the sections in the order PDF renders them.
func (w *pdfWriter) reportContents() []tocEntry {
	out := []tocEntry{{"summary", "Executive summary", 0}}
	if w.in.CodeReview != nil {
		out = append(out, tocEntry{"target", "Review target", 0})
		if w.in.Lifecycle != nil {
			out = append(out, tocEntry{"lifecycle", "Finding lifecycle", 0})
		}
	} else {
		out = append(out, tocEntry{"scope", "Scope and coverage", 0})
	}
	out = append(out, tocEntry{"findings", "Findings", 0})
	if !w.opt.Executive() {
		for i, fd := range w.findings {
			out = append(out, tocEntry{findingKey(fd), fmt.Sprintf("%d. %s", i+1, findingTitle(fd)), 1})
		}
		out = append(out, scannerContents(w.in)...)
		out = append(out, authzContents(w.in)...)
		if w.hasAppendix() {
			out = append(out, tocEntry{"appendix", "Appendix", 0})
		}
	}
	return append(out, tocEntry{"closing", "Limitations and provenance", 0})
}

func findingKey(fd *db.DBFinding) string { return fmt.Sprintf("finding-%d", fd.ID) }

// collectAppendix moves raw evidence out of findings whose trace is shown in
// structured form, and keeps the stored task instructions for the record.
func (w *pdfWriter) collectAppendix() {
	if w.opt.Executive() {
		return
	}
	for i, fd := range w.findings {
		if e := strings.TrimSpace(fd.Evidence); e != "" && parseEvidence(e).Parsed {
			w.appendix = append(w.appendix, appendixItem{fmt.Sprintf("Finding %d. %s (ID %d)", i+1, findingTitle(fd), fd.ID), e})
		}
	}
}

func (w *pdfWriter) hasAppendix() bool {
	return len(w.appendix) > 0 || strings.TrimSpace(w.in.Goal) != ""
}

func newPDFWriter(opt PDFOptions, total int, findings []*db.DBFinding, title, footerRef, version string, generatedAt time.Time) *pdfWriter {
	f := gofpdf.New("P", "mm", "A4", "")
	f.SetMargins(marginX, marginTop, marginX)
	f.SetAutoPageBreak(true, marginBottom)
	f.AddUTF8FontFromBytes("sans", "", fontSans)
	f.AddUTF8FontFromBytes("sans", "B", fontSansBold)
	f.AddUTF8FontFromBytes("mono", "", fontMono)
	w := &pdfWriter{f: f, opt: opt, total: total, findings: findings,
		pages: map[string]int{}, links: map[string]int{}}
	w.footerRef = w.t(footerRef)
	f.SetTitle(w.t(title), true)
	f.SetAuthor("Counterweight", true)
	f.SetCreator("Counterweight "+nz(version, "dev"), true)
	f.SetSubject("Penetration test report", true)
	f.SetCreationDate(generatedAt)
	f.SetModificationDate(generatedAt)
	f.SetCatalogSort(true) // stable resource order: identical input gives identical bytes
	f.SetHeaderFuncMode(w.header, false)
	f.SetFooterFunc(w.footer)
	return w
}

// reportFindings prefers the findings register and falls back to legacy
// exploration finding nodes.
func reportFindings(in Input) []*db.DBFinding {
	var out []*db.DBFinding
	if in.FindingRecords != nil {
		out = append(out, in.FindingRecords...)
	} else {
		for _, n := range in.Findings {
			v := parseFinding(n)
			out = append(out, &db.DBFinding{ID: n.ID, Name: v.Name, VulnClass: v.VulnClass,
				Severity: v.Severity, Summary: v.Summary, Evidence: v.PoC, CreatedAt: n.CreatedAt})
		}
	}
	sortFindingsForExport(out)
	return out
}

// --- text and drawing primitives ---

// t prepares stored or agent-authored text: redacts credentials (unless
// disabled) and removes characters the PDF fonts cannot encode.
func (w *pdfWriter) t(s string) string {
	if !w.opt.IncludeSecrets {
		s = RedactLocalPaths(RedactSecrets(s))
	}
	return pdfText(s)
}

// pdfText keeps UTF-8 text intact except control characters and runes outside
// the Basic Multilingual Plane, which the UTF-8 font tables cannot index.
func pdfText(s string) string {
	s = strings.ToValidUTF8(s, "?")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
		case r > 0xFFFF:
			b.WriteRune('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (w *pdfWriter) text(c rgb)                      { w.f.SetTextColor(c.r, c.g, c.b) }
func (w *pdfWriter) fill(c rgb)                      { w.f.SetFillColor(c.r, c.g, c.b) }
func (w *pdfWriter) stroke(c rgb)                    { w.f.SetDrawColor(c.r, c.g, c.b) }
func (w *pdfWriter) font(style string, size float64) { w.f.SetFont("sans", style, size) }

func (w *pdfWriter) ensure(h float64) {
	if w.f.GetY()+h > pageH-marginBottom {
		w.f.AddPage()
	}
}

func (w *pdfWriter) hairline() {
	y := w.f.GetY()
	w.stroke(cHair)
	w.f.SetLineWidth(0.25)
	w.f.Line(marginX, y, pageW-marginX, y)
}

// logo draws the Counterweight balance mark (docs/brand/counterweight-on-light.svg)
// with vector primitives: the broken ring, the heavy hexagon and the teal counterweight.
func (w *pdfWriter) logo(x, y, size float64, ink, accent rgb) {
	f := w.f
	s := size / 740.0
	px := func(v float64) float64 { return x + (v-250)*s }
	py := func(v float64) float64 { return y + (v-280)*s }
	cx, cy, r := px(613.9), py(648.0), 310*s
	f.SetLineCapStyle("round")
	f.SetLineJoinStyle("round")
	f.SetLineWidth(60 * s)
	w.stroke(ink)
	f.Arc(cx, cy, r, r, 0, 46, 248, "D")
	w.stroke(accent)
	f.Arc(cx, cy, r, r, 0, 263, 354, "D")
	poly := func(pts []float64, c rgb, lw float64) {
		var ps []gofpdf.PointType
		for i := 0; i+1 < len(pts); i += 2 {
			ps = append(ps, gofpdf.PointType{X: px(pts[i]), Y: py(pts[i+1])})
		}
		w.fill(c)
		w.stroke(c)
		f.SetLineWidth(lw * s)
		f.Polygon(ps, "FD")
	}
	poly([]float64{679.2, 683, 604.6, 812.2, 455.4, 812.2, 380.8, 683, 455.4, 553.8, 604.6, 553.8}, ink, 36)
	poly([]float64{964.1, 547, 926.1, 612.9, 849.9, 612.9, 811.9, 547, 849.9, 481.1, 926.1, 481.1}, accent, 24)
	f.SetLineCapStyle("butt")
	f.SetLineJoinStyle("miter")
	f.SetLineWidth(0.25)
}

func (w *pdfWriter) header() {
	f := w.f
	if f.PageNo() == 1 {
		return
	}
	w.logo(marginX, 9.2, 5.8, cInk, cTeal)
	w.font("B", 9)
	w.text(cInk)
	f.SetXY(marginX+7.5, 9.6)
	f.CellFormat(28, 5, "Counterweight", "", 0, "L", false, 0, "")
	w.font("", 8)
	w.text(cMuted)
	f.SetXY(pageW-marginX-110, 9.6)
	f.CellFormat(110, 5, w.section, "", 0, "R", false, 0, "")
	w.stroke(cHair)
	f.SetLineWidth(0.25)
	f.Line(marginX, 17, pageW-marginX, 17)
	f.SetXY(marginX, marginTop)
}

func (w *pdfWriter) footer() {
	f := w.f
	if f.PageNo() == 1 {
		return
	}
	y := pageH - 13
	w.stroke(cHair)
	f.SetLineWidth(0.25)
	f.Line(marginX, y, pageW-marginX, y)
	w.font("", 7.5)
	w.text(cMuted)
	f.SetXY(marginX, y+1.5)
	f.CellFormat(130, 4, "Confidential · Counterweight · "+w.footerRef, "", 0, "L", false, 0, "")
	page := fmt.Sprintf("Page %d", f.PageNo())
	if w.total > 0 {
		page = fmt.Sprintf("Page %d of %d", f.PageNo(), w.total)
	}
	f.SetXY(pageW-marginX-40, y+1.5)
	f.CellFormat(40, 4, page, "", 0, "R", false, 0, "")
}

func (w *pdfWriter) startSection(key, title, intro string, newPage bool) {
	f := w.f
	w.section = title
	if newPage {
		f.AddPage()
	} else {
		w.ensure(45)
		f.Ln(6)
	}
	w.mark(key, title, 0)
	w.font("B", 17)
	w.text(cInk)
	f.MultiCell(contentW, 8, title, "", "L", false)
	w.fill(cTeal)
	f.Rect(marginX+1, f.GetY()+0.8, 14, 0.8, "F")
	f.Ln(4)
	if intro != "" {
		w.font("", 9)
		w.text(cMuted)
		f.MultiCell(contentW, 4.6, intro, "", "L", false)
		f.Ln(2)
	}
}

// mark records where a navigable section starts: its page for the contents,
// a PDF bookmark, and the target of the contents entry's link.
func (w *pdfWriter) mark(key, title string, level int) {
	if key == "" {
		return
	}
	f := w.f
	w.pages[key] = f.PageNo()
	f.Bookmark(w.t(title), level, -1)
	if id, ok := w.links[key]; ok {
		f.SetLink(id, f.GetY(), -1)
	}
}

// contents prints the table of contents with the first pass's page numbers.
// Every entry links to its section.
func (w *pdfWriter) contents() {
	if w.opt.Executive() {
		return // a few pages; the bookmarks are enough
	}
	f := w.f
	w.section = "Contents"
	f.AddPage()
	w.font("B", 17)
	w.text(cInk)
	f.MultiCell(contentW, 8, "Contents", "", "L", false)
	w.fill(cTeal)
	f.Rect(marginX+1, f.GetY()+0.8, 14, 0.8, "F")
	f.Ln(6)
	for _, e := range w.tocEntries {
		w.ensure(8)
		indent := float64(e.level) * 7
		if e.level == 0 {
			w.font("B", 10)
			w.text(cInk)
			f.Ln(1.2)
		} else {
			w.font("", 9)
			w.text(cInk2)
		}
		title := w.t(e.title)
		if lines := f.SplitText(title, contentW-indent-16); len(lines) > 1 {
			title = strings.TrimSpace(lines[0]) + "…"
		}
		page := ""
		if n := w.prevPages[e.key]; n > 0 {
			page = fmt.Sprint(n)
		}
		link := f.AddLink()
		w.links[e.key] = link
		y := f.GetY()
		f.SetXY(marginX+indent, y)
		f.CellFormat(contentW-indent-16, 6.2, title, "", 0, "L", false, link, "")
		f.SetXY(pageW-marginX-16, y)
		f.CellFormat(16, 6.2, page, "", 1, "R", false, link, "")
		w.stroke(cHair)
		f.SetLineWidth(0.15)
		f.Line(marginX+indent, f.GetY(), pageW-marginX, f.GetY())
	}
}

func (w *pdfWriter) subheading(title string) {
	w.ensure(26)
	w.f.Ln(2.5)
	w.font("B", 10.5)
	w.text(cInk)
	w.f.MultiCell(contentW, 5.6, title, "", "L", false)
	w.f.Ln(0.8)
}

func (w *pdfWriter) paragraph(s string) {
	w.font("", 9)
	w.text(cInk)
	w.f.MultiCell(contentW, 4.7, s, "", "L", false)
	w.f.Ln(1.2)
}

func (w *pdfWriter) label(s string) {
	w.ensure(32)
	w.f.Ln(1)
	w.font("B", 8)
	w.text(cMuted)
	w.f.CellFormat(contentW, 4.6, strings.ToUpper(s), "", 1, "L", false, 0, "")
}

// code renders a monospace block, capped to keep one artefact from dominating the document.
func (w *pdfWriter) code(s string) {
	s, cut := capText(strings.TrimRight(s, "\n"), evidenceLineCap, evidenceCharCap)
	f := w.f
	f.SetFont("mono", "", 7.4)
	w.text(cInk2)
	w.fill(cSunk)
	f.SetCellMargin(2.2)
	f.MultiCell(contentW, 3.7, s, "", "L", true)
	f.SetCellMargin(1)
	if cut {
		w.font("", 7.5)
		w.text(cMuted)
		f.MultiCell(contentW, 4, "Truncated in the PDF. Full content is in the Markdown, ZIP and JSON exports.", "", "L", false)
	}
	f.Ln(1.5)
}

func capText(s string, maxLines, maxChars int) (string, bool) {
	cut := false
	if r := []rune(s); len(r) > maxChars {
		s, cut = string(r[:maxChars]), true
	}
	if lines := strings.Split(s, "\n"); len(lines) > maxLines {
		s, cut = strings.Join(lines[:maxLines], "\n"), true
	}
	return s, cut
}

func (w *pdfWriter) callout(title, body string, bg, fg rgb) {
	f := w.f
	w.ensure(18)
	w.fill(bg)
	f.SetCellMargin(3)
	w.font("B", 9)
	w.text(fg)
	f.MultiCell(contentW, 5.4, title, "", "L", true)
	w.font("", 8.6)
	w.text(cInk)
	f.MultiCell(contentW, 4.4, body, "", "L", true)
	f.SetCellMargin(1)
	f.Ln(3)
}

type metric struct {
	value, label string
	color        rgb
}

func (w *pdfWriter) metrics(y float64, items []metric) float64 {
	f := w.f
	gap := 3.0
	bw := (contentW - gap*float64(len(items)-1)) / float64(len(items))
	const bh = 19.0
	for i, m := range items {
		x := marginX + float64(i)*(bw+gap)
		w.fill(cSurface)
		w.stroke(cHair)
		f.SetLineWidth(0.25)
		f.RoundedRect(x, y, bw, bh, 1.6, "1234", "FD")
		w.font("B", 16)
		w.text(m.color)
		f.SetXY(x+2, y+2.6)
		f.CellFormat(bw-4, 8, m.value, "", 0, "L", false, 0, "")
		w.font("", 7)
		w.text(cMuted)
		f.SetXY(x+2, y+11.6)
		f.CellFormat(bw-4, 4, strings.ToUpper(m.label), "", 0, "L", false, 0, "")
	}
	f.SetXY(marginX, y+bh+4)
	return y + bh
}

func (w *pdfWriter) fact(label, value string) {
	f := w.f
	w.ensure(7)
	y := f.GetY()
	w.font("B", 8)
	w.text(cMuted)
	f.SetXY(marginX, y)
	f.CellFormat(42, 4.8, label, "", 0, "L", false, 0, "")
	w.font("", 8.6)
	w.text(cInk)
	f.SetXY(marginX+42, y)
	f.MultiCell(contentW-42, 4.8, value, "", "L", false)
	w.stroke(cHair)
	f.SetLineWidth(0.2)
	f.Line(marginX, f.GetY()+0.6, pageW-marginX, f.GetY()+0.6)
	f.SetY(f.GetY() + 1.6)
}

type column struct {
	title string
	width float64
	mono  bool
}

const tableCellLineCap = 14

func (w *pdfWriter) table(cols []column, rows [][]string) { w.linkedTable(cols, rows, nil) }

// linkedTable is table with optional per-cell URLs (links[row][col], "" for none).
// Linked cells are drawn in the brand teal and are clickable.
func (w *pdfWriter) linkedTable(cols []column, rows [][]string, links [][]string) {
	f := w.f
	linkAt := func(r, c int) string {
		if r < len(links) && c < len(links[r]) {
			return links[r][c]
		}
		return ""
	}
	const lineH = 4.1
	colFont := func(c column) {
		if c.mono {
			f.SetFont("mono", "", 7.2)
		} else {
			w.font("", 8.2)
		}
	}
	head := func() {
		w.fill(cSunk)
		w.font("B", 7.6)
		w.text(cInk2)
		x := marginX
		y := f.GetY()
		for _, c := range cols {
			f.SetXY(x, y)
			f.CellFormat(c.width, 6, strings.ToUpper(c.title), "", 0, "L", true, 0, "")
			x += c.width
		}
		f.SetXY(marginX, y+6)
	}
	w.ensure(14)
	head()
	for ri, row := range rows {
		cells := make([][]string, len(cols))
		n := 1
		for i, c := range cols {
			colFont(c)
			v := ""
			if i < len(row) {
				v = row[i]
			}
			lines := f.SplitText(v, c.width)
			if len(lines) > tableCellLineCap {
				lines = append(lines[:tableCellLineCap-1], "…")
			}
			cells[i] = lines
			if len(lines) > n {
				n = len(lines)
			}
		}
		h := float64(n)*lineH + 2
		if f.GetY()+h > pageH-marginBottom {
			f.AddPage()
			head()
		}
		y, x := f.GetY(), marginX
		for i, c := range cols {
			colFont(c)
			url := linkAt(ri, i)
			w.text(cInk)
			if url != "" {
				w.text(cTeal)
			}
			for j, line := range cells[i] {
				f.SetXY(x, y+1+float64(j)*lineH)
				f.CellFormat(c.width, lineH, line, "", 0, "L", false, 0, url)
			}
			x += c.width
		}
		w.text(cInk)
		w.stroke(cHair)
		f.SetLineWidth(0.2)
		f.Line(marginX, y+h, pageW-marginX, y+h)
		f.SetXY(marginX, y+h)
	}
	f.Ln(3)
}

func (w *pdfWriter) badge(x, y float64, sev string) float64 {
	f := w.f
	label := strings.ToUpper(normSeverity(sev))
	w.font("B", 7)
	bw := f.GetStringWidth(label) + 4
	w.fill(severityColor(sev))
	f.RoundedRect(x, y, bw, 4.6, 0.9, "1234", "F")
	w.text(cWhite)
	f.SetXY(x, y+0.2)
	f.CellFormat(bw, 4.2, label, "", 0, "C", false, 0, "")
	return bw
}

// --- sections ---

// openReviews counts findings still awaiting a reviewer decision.
func (w *pdfWriter) openReviews() int {
	n := 0
	for _, fd := range w.findings {
		if s := nz(fd.Status, db.FindingPending); s == db.FindingPending || s == db.FindingInProgress {
			n++
		}
	}
	return n
}

func (w *pdfWriter) severityCounts() map[string]int {
	counts := map[string]int{}
	for _, fd := range w.findings {
		s := normSeverity(fd.Severity)
		if _, ok := sevColors[s]; !ok {
			s = "info"
		}
		counts[s]++
	}
	return counts
}

func (w *pdfWriter) assetTotal() int {
	n := 0
	for _, c := range w.in.AssetCounts {
		n += c
	}
	return n
}

func (w *pdfWriter) coverage() string {
	c := w.in.Coverage
	if c == nil {
		return "Coverage tracking is disabled for this task."
	}
	if c.Denominator == 0 {
		return "No assets were counted for coverage."
	}
	return fmt.Sprintf("%d of %d assets have recorded results (%d%%). Approximate asset coverage, not a completed security test suite.",
		c.Tested, c.Denominator, c.Tested*100/c.Denominator)
}

func when(t time.Time) string {
	if t.IsZero() {
		return "Not recorded"
	}
	return t.Format("2006-01-02 15:04:05")
}

// coverSpec is what differs between the task report and findings export covers.
type coverSpec struct {
	kicker, title, subtitle string
	draft                   string // Badge text; empty hides it.
	metrics                 []metric
	meta                    [][2]string
}

func (w *pdfWriter) taskCover() {
	draft := ""
	if !w.exec.Final() || w.openReviews() > 0 {
		draft = "DRAFT · REVIEW PENDING OR TASK UNFINISHED"
	}
	counts := w.severityCounts()
	if cr := w.in.CodeReview; cr != nil {
		w.cover(coverSpec{
			kicker:   w.kicker("SECURITY CODE REVIEW  /  WHITE-BOX ASSESSMENT"),
			title:    nz(w.in.Title, "Code review: "+cr.Repository),
			subtitle: w.exec.Headline + ". " + w.exec.Detail,
			draft:    draft,
			metrics: []metric{
				{fmt.Sprint(len(w.findings)), "Findings", cInk},
				{fmt.Sprint(counts["critical"] + counts["high"]), "Critical + high", severityColor("critical")},
				{fmt.Sprint(w.openReviews()), "Awaiting review", cInk},
				{fmt.Sprint(len(citedFiles(w.findings))), "Files cited", cTeal},
			},
			meta: [][2]string{
				{"Repository", cr.Repository},
				{"Commit", cr.shortCommit()},
				{"Task status", taskStatusLabel(w.in.Status)},
				{"Review mode", reviewModeLabel(cr.Mode)},
				{"Generated", when(w.in.GeneratedAt)},
				{"Document", w.in.DocumentID()},
			},
		})
		return
	}
	cov := "n/a"
	if c := w.in.Coverage; c != nil && c.Denominator > 0 {
		cov = fmt.Sprintf("%d%%", c.Tested*100/c.Denominator)
	}
	w.cover(coverSpec{
		kicker:   w.kicker("PENETRATION TEST REPORT  /  ASSESSMENT RECORD"),
		title:    nz(w.in.Title, "Untitled task"),
		subtitle: w.exec.Headline + ". " + w.exec.Detail,
		draft:    draft,
		metrics: []metric{
			{fmt.Sprint(len(w.findings)), "Findings", cInk},
			{fmt.Sprint(counts["critical"] + counts["high"]), "Critical + high", severityColor("critical")},
			{fmt.Sprint(w.assetTotal()), "Assets", cInk},
			{cov, "Coverage", cTeal},
		},
		meta: [][2]string{
			{"Task ID", nz(w.t(w.in.TaskID), "-")},
			{"Generated", when(w.in.GeneratedAt)},
			{"Task status", taskStatusLabel(w.in.Status)},
			{"Version", "Counterweight " + nz(w.in.Version, "dev")},
			{"Document", w.in.DocumentID()},
		},
	})
}

func (w *pdfWriter) kicker(s string) string {
	if w.opt.Executive() {
		return s + "  /  EXECUTIVE SUMMARY"
	}
	return s
}

// cover draws the branded first page. Title and subtitle are passed through t().
func (w *pdfWriter) cover(spec coverSpec) {
	f := w.f
	f.AddPage()
	w.fill(cGround)
	f.Rect(0, 0, pageW, pageH, "F")
	w.fill(cTeal)
	f.Rect(0, 0, 3.2, pageH, "F")

	w.logo(marginX, 22, 13, cInk, cTeal)
	w.font("B", 14)
	w.text(cInk)
	f.SetXY(marginX+16, 25.6)
	f.CellFormat(80, 6, "Counterweight", "", 0, "L", false, 0, "")

	f.SetXY(marginX, 74)
	w.font("B", 8.5)
	w.text(cTeal)
	f.CellFormat(contentW, 5, spec.kicker, "", 1, "L", false, 0, "")
	f.Ln(2)
	w.font("B", 23)
	w.text(cInk)
	title := []rune(w.t(spec.title))
	if len(title) > 180 {
		title = append(title[:177], '…')
	}
	f.MultiCell(contentW, 10, string(title), "", "L", false)
	f.Ln(3)
	w.font("", 10.5)
	w.text(cInk2)
	f.MultiCell(contentW, 5.4, w.t(spec.subtitle), "", "L", false)
	if spec.draft != "" {
		f.Ln(3)
		w.font("B", 8.5)
		w.fill(cInk)
		w.text(cGround)
		f.SetCellMargin(2.5)
		f.CellFormat(f.GetStringWidth(spec.draft)+5, 6, spec.draft, "", 1, "L", true, 0, "")
		f.SetCellMargin(1)
	}

	y := f.GetY() + 12
	if y < 152 {
		y = 152
	}
	w.metrics(y, spec.metrics)

	f.SetY(226)
	w.stroke(cInk)
	f.SetLineWidth(0.35)
	f.Line(marginX, 224, pageW-marginX, 224)
	for i, m := range spec.meta {
		x := marginX + float64(i%2)*(contentW/2)
		yy := 228 + float64(i/2)*9
		w.font("B", 7.5)
		w.text(cMuted)
		f.SetXY(x, yy)
		f.CellFormat(contentW/2, 4, strings.ToUpper(m[0]), "", 0, "L", false, 0, "")
		w.font("", 9)
		w.text(cInk)
		f.SetXY(x, yy+4)
		f.CellFormat(contentW/2, 4.6, m[1], "", 0, "L", false, 0, "")
	}
	f.SetXY(marginX, 262)
	w.font("", 7.5)
	w.text(cMuted)
	f.MultiCell(contentW, 3.9, "Confidential. Generated from the stored records of authorized security testing. "+
		"It contains security-sensitive material: handle and share it accordingly. "+w.redactionNote(), "", "L", false)
}

func (w *pdfWriter) redactionNote() string {
	if w.opt.IncludeSecrets {
		return "Credential and local-path redaction was disabled for this export."
	}
	return "Credentials matching common patterns are shown as " + Redacted + ", the review checkout as " + CheckoutPlaceholder +
		" and home directories as " + HomePlaceholder + "; redaction is pattern-based, so review before sharing."
}

func (w *pdfWriter) summary() {
	w.startSection("summary", "Executive summary", "What the stored records establish for this task, and what they do not.", true)
	if !w.exec.Final() {
		w.callout(w.exec.Headline, w.exec.Detail, cWarnBg, cWarn)
	}
	w.severityMetrics()

	w.subheading("Objective")
	w.paragraph(w.t(nz(w.in.Objective(), "(not specified)")))
	if len(w.findings) > 0 {
		w.paragraph(posture(w.in))
	}

	if actions := keyActions(w.in, 3); len(actions) > 0 {
		w.subheading("Priorities")
		for i, a := range actions {
			w.priority(i+1, a)
		}
	}

	w.reviewStatus()

	w.subheading("At a glance")
	w.fact("Task status", taskStatusLabel(w.in.Status))
	w.fact("Execution", w.exec.Headline)
	w.fact("Execution isolation", w.in.ExecutionIsolation().Description())
	if cr := w.in.CodeReview; cr != nil {
		w.fact("Repository", w.t(cr.URL))
		w.fact("Files cited", fmt.Sprint(len(citedFiles(w.findings))))
		return
	}
	w.fact("Assets", fmt.Sprint(w.assetTotal()))
}

// priority draws one prioritized action: a severity badge, the finding and its next step.
func (w *pdfWriter) priority(n int, a KeyAction) {
	f := w.f
	w.ensure(16)
	y := f.GetY()
	w.font("B", 9)
	w.text(cMuted)
	f.SetXY(marginX, y)
	f.CellFormat(6, 4.8, fmt.Sprint(n), "", 0, "L", false, 0, "")
	bw := w.badge(marginX+6, y+0.1, a.Severity)
	w.font("B", 9)
	w.text(cInk)
	f.SetXY(marginX+8+bw, y)
	f.MultiCell(contentW-8-bw, 4.8, w.t(a.Title), "", "L", false)
	w.font("", 8.6)
	w.text(cInk2)
	f.SetX(marginX + 8 + bw)
	f.MultiCell(contentW-8-bw, 4.4, a.Action, "", "L", false)
	f.Ln(2)
}

// reviewTarget replaces network scope and asset coverage for white-box reviews.
func (w *pdfWriter) reviewTarget(cr *CodeReview) {
	w.startSection("target", "Review target", "The source code this review covered and the code locations its findings cite.", !w.opt.Executive())
	w.fact("Repository", w.t(cr.URL))
	w.fact("Ref", w.t(nz(cr.Ref, "Default branch (not pinned)")))
	commit := "Not recorded"
	if cr.Commit != "" {
		commit = w.t(cr.Commit)
	}
	w.fact("Commit", commit)
	w.fact("Review mode", reviewModeLabel(cr.Mode))
	w.fact("Method", "Source code review only. No traffic is sent to live hosts or networks as part of the review.")
	w.fact("Target details", cr.targetSource())

	w.subheading("Code cited by findings")
	files := citedFiles(w.findings)
	if len(files) == 0 {
		w.paragraph("No finding cites a file:line location.")
		return
	}
	var rows, links [][]string
	for _, cf := range files {
		rows = append(rows, []string{w.t(cf.File), w.t(strings.Join(cf.Lines, ", ")), fmt.Sprint(cf.Findings)})
		links = append(links, []string{cr.SourceLink(cf.File, ""), "", ""})
	}
	w.linkedTable([]column{{"File", 92, true}, {"Lines", 60, true}, {"Findings", 22, false}}, rows, links)
	w.paragraph("File-level review coverage is not tracked yet: a file not listed here is not established as reviewed or free of issues.")
}

// lifecycle shows, per finding, how far it went after it was reported.
func (w *pdfWriter) lifecycle() {
	w.startSection("lifecycle", "Finding lifecycle", lifecycleIntro, !w.opt.Executive())
	var confirmed, falsePositive, merged, validated int
	for _, fd := range w.findings {
		l := w.in.Lifecycle[fd.ID]
		switch l.Verification {
		case "confirmed":
			confirmed++
		case "rejected":
			falsePositive++
		}
		if l.PRState == "merged" {
			merged++
		}
		if l.Stage() == StageValidated {
			validated++
		}
	}
	w.metrics(w.f.GetY()+1, []metric{
		{fmt.Sprint(len(w.findings)), "Reported", cInk},
		{fmt.Sprint(confirmed), "Confirmed", cInk2},
		{fmt.Sprint(falsePositive), "False positives", cMuted},
		{fmt.Sprint(merged), "Fixes merged", cTeal},
		{fmt.Sprint(validated), "Validated fixes", cTeal},
	})
	var rows, links [][]string
	for _, fd := range w.findings {
		l := w.in.Lifecycle[fd.ID]
		rows = append(rows, []string{fmt.Sprint(fd.ID), w.t(findingTitle(fd)), verificationLabel(l.Verification), fixLabel(l),
			recheckLabel(l), strings.ReplaceAll(nz(lifecycleDates(l), "-"), ", ", "\n")})
		links = append(links, []string{"", "", "", l.PRURL, "", ""})
	}
	if len(rows) > 0 {
		w.linkedTable([]column{{"ID", 11, true}, {"Finding", 58, false}, {"Verification", 23, false}, {"Fix", 24, false},
			{"After merge", 28, false}, {"Dates", 30, false}}, rows, links)
	}
	w.paragraph(lifecycleNote)
}

func (w *pdfWriter) severityMetrics() {
	counts := w.severityCounts()
	w.metrics(w.f.GetY()+1, []metric{
		{fmt.Sprint(counts["critical"]), "Critical", severityColor("critical")},
		{fmt.Sprint(counts["high"]), "High", severityColor("high")},
		{fmt.Sprint(counts["medium"]), "Medium", severityColor("medium")},
		{fmt.Sprint(counts["low"]), "Low", severityColor("low")},
		{fmt.Sprint(counts["info"]), "Info / other", cMuted},
	})
}

func (w *pdfWriter) reviewStatus() {
	w.subheading("Review status")
	status := map[string]int{}
	for _, fd := range w.findings {
		status[nz(fd.Status, db.FindingPending)]++
	}
	w.paragraph(fmt.Sprintf("%d of %d still await a reviewer decision (pending or in review). "+
		"Severities are assigned when a finding is recorded and do not, on their own, establish exploitability or business impact.", w.openReviews(), len(w.findings)))
	var rows [][]string
	for _, s := range reviewStatusOrder {
		if status[s] > 0 {
			rows = append(rows, []string{findingStatusLabel(s), fmt.Sprint(status[s])})
			delete(status, s)
		}
	}
	for _, s := range sortedKeys(status) { // unknown statuses, in stable order
		rows = append(rows, []string{w.t(findingStatusLabel(s)), fmt.Sprint(status[s])})
	}
	if len(rows) > 0 {
		w.table([]column{{"Status", 120, false}, {"Findings", 54, false}}, rows)
	}
}

func (w *pdfWriter) scope() {
	w.startSection("scope", "Scope and coverage", "Authorized scope rows recorded for this task and the assets discovered within it.", false)
	if len(w.in.Scope) == 0 {
		w.paragraph("No scope rows were recorded for this task. Review the authorized scope separately.")
	} else {
		var rows [][]string
		for _, s := range w.in.Scope {
			target := nz(s.Domain, nz(s.Net, nz(s.CompanyName, nz(s.Value, s.Kind))))
			rows = append(rows, []string{w.t(s.Kind), w.t(target), w.t(s.Source)})
		}
		w.table([]column{{"Kind", 30, false}, {"Target", 104, true}, {"Source", 40, false}}, rows)
	}
	if len(w.in.AssetCounts) > 0 {
		w.subheading("Assets by type")
		var rows [][]string
		for _, k := range sortedKeys(w.in.AssetCounts) {
			rows = append(rows, []string{w.t(k), fmt.Sprint(w.in.AssetCounts[k])})
		}
		w.table([]column{{"Type", 120, false}, {"Assets", 54, false}}, rows)
	}
	w.subheading("Coverage")
	w.paragraph(w.coverage())
}

func (w *pdfWriter) findingsSection(intro, empty string) {
	f := w.f
	w.startSection("findings", "Findings", intro, !w.opt.Executive())
	if len(w.findings) == 0 {
		w.paragraph(empty)
		return
	}
	var rows [][]string
	for i, fd := range w.findings {
		rows = append(rows, []string{fmt.Sprint(i + 1), strings.ToUpper(normSeverity(fd.Severity)), w.t(findingTitle(fd)),
			w.t(classShort(classInfo(fd.VulnClass))), findingStatusLabel(fd.Status)})
	}
	w.table([]column{{"#", 8, false}, {"Severity", 19, false}, {"Finding", 84, false}, {"Class", 36, false}, {"Status", 27, false}}, rows)
	if w.opt.Executive() {
		w.paragraph("This executive summary lists findings only. The technical report carries each finding's code trace, evidence and remediation detail.")
		return
	}

	for i, fd := range w.findings {
		f.AddPage()
		w.mark(findingKey(fd), fmt.Sprintf("%d. %s", i+1, findingTitle(fd)), 1)
		y := f.GetY()
		bw := w.badge(marginX, y, fd.Severity)
		ci := classInfo(fd.VulnClass)
		f.SetFont("mono", "", 7.6)
		w.text(cMuted)
		f.SetXY(marginX+bw+3, y+0.2)
		ref := fmt.Sprintf("#%d  ·  Finding %d", i+1, fd.ID)
		if ci.CWE != "" {
			ref += "  ·  " + ci.CWE
		}
		f.CellFormat(contentW-bw-3, 4.4, ref, "", 1, "L", false, 0, "")
		f.Ln(1.5)
		w.font("B", 13)
		w.text(cInk)
		f.MultiCell(contentW, 6.2, w.t(findingTitle(fd)), "", "L", false)
		f.Ln(1)

		var lp *FindingLifecycle
		if w.in.Lifecycle != nil {
			l := w.in.Lifecycle[fd.ID]
			lp = &l
		}
		w.fact("Status", findingStatusLabel(fd.Status))
		if ci.Label != "" {
			w.fact("Class", w.t(ci.Label))
		}
		if ci.CWE != "" || ci.OWASP != "" {
			w.fact("Reference", strings.Trim(ci.CWE+"  ·  "+ci.OWASP, " ·"))
		}
		if !fd.CreatedAt.IsZero() {
			w.fact("Found", fd.CreatedAt.Format("2006-01-02 15:04"))
		}
		if lp != nil {
			stage := stageLabel(lp.Stage())
			if d := lifecycleDates(*lp); d != "" {
				stage += " (" + d + ")"
			}
			w.fact("Lifecycle", stage)
		}
		if w.in.CodeReview == nil || len(fd.TrafficBindings) > 0 {
			w.fact("Traffic evidence", fmt.Sprint(len(fd.TrafficBindings)))
		}
		if w.showTask {
			w.fact("Task", w.t(findingTaskLabel(fd)))
		}
		f.Ln(1)
		if !closed(fd, lp) {
			w.callout("Next step", nextAction(fd, lp), cSunk, cTeal)
		}

		if s := strings.TrimSpace(fd.Summary); s != "" {
			w.label("Overview")
			w.markdown(w.t(s))
		}
		if locations := codeLocations(fd); len(locations) > 0 {
			w.label("Affected code")
			var rows, links [][]string
			for _, l := range locations {
				rows = append(rows, []string{w.t(l.File), strings.Join(l.Lines, ", ")})
				links = append(links, []string{w.in.CodeReview.SourceLink(l.File, firstRange(l.Lines)), ""})
			}
			w.linkedTable([]column{{"File", 120, true}, {"Lines", 54, true}}, rows, links)
		}
		if e := strings.TrimSpace(fd.Evidence); e != "" {
			if tr := parseEvidence(e); tr.Parsed {
				w.trace(tr)
			} else {
				if w.in.CodeReview != nil {
					w.label("Code evidence")
				} else {
					w.label("Recorded evidence")
				}
				w.code(w.t(e))
			}
		}
		if len(fd.TrafficBindings) > 0 || (fd.Report != "" && fd.EvidenceVersion != fd.ReportEvidenceVersion) {
			w.label("Linked traffic evidence")
			if fd.Report != "" && fd.EvidenceVersion != fd.ReportEvidenceVersion {
				w.callout("Detailed report is stale", "Linked evidence changed after the detailed report was written; the report needs updating.", cWarnBg, cWarn)
			}
			var rows [][]string
			for _, b := range fd.TrafficBindings {
				target := w.t(b.Snapshot.URL)
				if note := strings.TrimSpace(b.Note); note != "" {
					target += "\n" + w.t(note)
				}
				rows = append(rows, []string{fmt.Sprint(b.ID), w.t(b.Role), w.t(b.Snapshot.Method), target, fmt.Sprint(b.Snapshot.Status)})
			}
			if len(rows) > 0 {
				w.table([]column{{"Evidence", 18, true}, {"Role", 22, false}, {"Method", 16, true}, {"URL / note", 104, true}, {"Status", 14, true}}, rows)
			}
		}
		if rep := strings.TrimSpace(fd.Report); rep != "" {
			w.label("Detailed report")
			w.markdown(w.t(rep))
		}
	}
}

func firstRange(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// classShort reads "SQL injection · CWE-89" for compact table cells.
func classShort(c ClassInfo) string {
	if c.Label == "" {
		return "-"
	}
	if c.CWE == "" {
		return c.Label
	}
	return c.Label + " · " + c.CWE
}

// trace renders a parsed source-to-sink trace as numbered steps, then its
// labelled fields (root cause, remediation and so on).
func (w *pdfWriter) trace(tr EvidenceTrace) {
	w.label("Code trace")
	var rows, links [][]string
	for i, st := range tr.Steps {
		rows = append(rows, []string{fmt.Sprint(i + 1), humanize(st.Kind), w.t(st.Location), w.t(st.Detail)})
		link := ""
		if file, lines, ok := strings.Cut(st.Location, ":"); ok {
			link = w.in.CodeReview.SourceLink(file, lines)
		}
		links = append(links, []string{"", "", link, ""})
	}
	w.linkedTable([]column{{"#", 7, false}, {"Step", 24, false}, {"Location", 46, true}, {"What happens", 97, false}}, rows, links)
	for _, fl := range tr.Fields {
		if fl.Label == "Fingerprint" || fl.Label == "Ref" {
			continue // identifiers stay in the appendix with the raw evidence
		}
		w.fact(fl.Label, w.t(fl.Text))
	}
	w.font("", 7.5)
	w.text(cMuted)
	w.f.MultiCell(contentW, 4, "The recorded evidence this trace was read from is reproduced in the appendix.", "", "L", false)
	w.f.Ln(1)
}

// appendixSection reproduces raw evidence moved out of finding pages and the
// stored task instructions.
func (w *pdfWriter) appendixSection() {
	if w.opt.Executive() || !w.hasAppendix() {
		return
	}
	w.startSection("appendix", "Appendix", "Recorded material kept for reference and audit.", true)
	if len(w.appendix) > 0 {
		w.subheading("Recorded evidence")
		for _, a := range w.appendix {
			w.label(w.t(a.ref))
			w.code(w.t(a.body))
		}
	}
	if g := strings.TrimSpace(w.in.Goal); g != "" {
		w.subheading("Task instructions")
		w.paragraph("The instructions the task was started with, as stored.")
		w.code(w.t(g))
	}
}

func (w *pdfWriter) closing() {
	w.startSection("closing", "Limitations and provenance", "How this document was produced and what it cannot establish.", true)
	w.subheading("Limitations and remaining review")
	notes := append([]string(nil), limitationNotes...)
	cr := w.in.CodeReview
	if cr != nil {
		notes = append([]string(nil), codeReviewNotes...)
	}
	if w.in.Truncated {
		notes = append(notes, "Record limit reached: each section includes at most 5,000 newest records. Older records remain in the application; this export is incomplete.")
	}
	w.limitations(notes)

	w.subheading("Provenance")
	w.fact("Document ID", w.in.DocumentID())
	w.fact("Task ID", nz(w.t(w.in.TaskID), "-"))
	w.fact("Task status", taskStatusLabel(w.in.Status))
	if cr != nil {
		w.fact("Repository", w.t(cr.URL))
		w.fact("Ref", w.t(nz(cr.Ref, "Default branch (not pinned)")))
		w.fact("Commit", w.t(nz(cr.Commit, "Not recorded")))
		w.fact("Review mode", reviewModeLabel(cr.Mode))
		w.fact("Target details", cr.targetSource())
	}
	w.fact("Created", when(w.in.CreatedAt))
	if !w.in.FirstRunAt.IsZero() { // recorded only for tasks with a time limit
		w.fact("First run", when(w.in.FirstRunAt))
	}
	completed := when(w.in.CompletedAt)
	if terminal := w.in.Status == "done" || w.in.Status == "failed" || w.in.Status == "timeout"; w.in.CompletedAt.IsZero() && !terminal {
		completed = "Not completed"
	}
	w.fact("Completed", completed)
	w.fact("Generated", when(w.in.GeneratedAt))
	if w.in.IncludeModels {
		models := "Not recorded"
		if len(w.in.Models) > 0 {
			models = w.t(strings.Join(w.in.Models, "; "))
		}
		w.fact("Models used", models)
	}
	w.fact("Software", "Counterweight "+nz(w.in.Version, "dev"))
	w.fact("Fingerprint", w.in.ContentFingerprint())
	if cr != nil {
		w.fact("Source", "Stored task records: findings register and review target.")
		return
	}
	w.fact("Source", "Stored task records: findings register, scope, coverage and linked traffic evidence.")
}

// limitations lists document-specific notes followed by the PDF-wide ones.
func (w *pdfWriter) limitations(notes []string) {
	notes = append(notes, "Evidence blocks longer than the PDF limit are truncated, and characters the report fonts do not cover (for example CJK or emoji) may not display; the Markdown, ZIP and JSON exports keep the full text.", w.redactionNote(),
		"The fingerprint is a SHA-256 over the reported findings (identity, rating, status and recorded text). An export with the same fingerprint reports the same findings.")
	for _, n := range notes {
		w.bullet("•", n)
	}
}

func (w *pdfWriter) bullet(marker, s string) {
	f := w.f
	w.ensure(8)
	w.font("", 9)
	w.text(cInk)
	y := f.GetY()
	f.SetXY(marginX, y)
	f.CellFormat(6, 4.7, marker, "", 0, "L", false, 0, "")
	f.SetXY(marginX+6, y)
	f.MultiCell(contentW-6, 4.7, s, "", "L", false)
	f.Ln(0.8)
}

// --- Markdown subset for stored detailed reports ---

var (
	mdLink    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	mdEmph    = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)
	mdCode    = regexp.MustCompile("`([^`]+)`")
	mdOrdered = regexp.MustCompile(`^(\d+)[.)]\s+(.*)$`)
	mdTableSp = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?$`)
)

func mdInline(s string) string {
	s = mdLink.ReplaceAllString(s, "$1 ($2)")
	s = mdEmph.ReplaceAllString(s, "$2")
	return mdCode.ReplaceAllString(s, "$1")
}

func mdCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = mdInline(strings.TrimSpace(parts[i]))
	}
	return parts
}

// markdown renders headings, paragraphs, lists, quotes, rules, fenced code and
// pipe tables. Anything else is shown as plain text, never dropped.
func (w *pdfWriter) markdown(src string) {
	var para, code, table []string
	inCode := false
	flushPara := func() {
		if len(para) > 0 {
			w.paragraph(mdInline(strings.Join(para, " ")))
			para = nil
		}
	}
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		head := mdCells(table[0])
		var rows [][]string
		for _, l := range table[1:] {
			if mdTableSp.MatchString(strings.TrimSpace(l)) {
				continue
			}
			rows = append(rows, mdCells(l))
		}
		cols := make([]column, len(head))
		for i, h := range head {
			cols[i] = column{h, contentW / float64(len(head)), false}
		}
		w.table(cols, rows)
		table = nil
	}
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if inCode {
				w.code(strings.Join(code, "\n"))
				code, inCode = nil, false
			} else {
				flushPara()
				flushTable()
				inCode = true
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		if strings.HasPrefix(t, "|") {
			flushPara()
			table = append(table, t)
			continue
		}
		flushTable()
		switch {
		case t == "":
			flushPara()
		case strings.HasPrefix(t, "#"):
			flushPara()
			level := len(t) - len(strings.TrimLeft(t, "#"))
			text := mdInline(strings.TrimSpace(strings.TrimLeft(t, "#")))
			w.ensure(30)
			w.f.Ln(1.5)
			size := 10.5
			if level >= 3 {
				size = 9.5
			}
			w.font("B", size)
			w.text(cInk)
			w.f.MultiCell(contentW, 5.2, text, "", "L", false)
			w.f.Ln(0.6)
		case t == "---" || t == "***" || t == "___":
			flushPara()
			w.f.Ln(1)
			w.hairline()
			w.f.Ln(2)
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ "):
			flushPara()
			w.bullet("•", mdInline(t[2:]))
		case mdOrdered.MatchString(t):
			flushPara()
			m := mdOrdered.FindStringSubmatch(t)
			w.bullet(m[1]+".", mdInline(m[2]))
		case strings.HasPrefix(t, ">"):
			flushPara()
			w.font("", 9)
			w.text(cMuted)
			w.f.SetX(marginX + 4)
			w.f.MultiCell(contentW-4, 4.7, mdInline(strings.TrimSpace(strings.TrimLeft(t, ">"))), "", "L", false)
		default:
			para = append(para, t)
		}
	}
	if inCode {
		w.code(strings.Join(code, "\n"))
	}
	flushTable()
	flushPara()
}
