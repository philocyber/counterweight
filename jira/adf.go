package jira

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Atlassian Document Format (ADF) is the JSON document model the v3 API uses for
// descriptions and comments. Node builds one element; Doc wraps a document.

// Node is one ADF node.
type Node = map[string]any

// Doc is a complete ADF document.
type Doc = map[string]any

// NewDoc wraps block nodes in a document.
func NewDoc(blocks ...Node) Doc {
	if blocks == nil {
		blocks = []Node{}
	}
	return Doc{"type": "doc", "version": 1, "content": blocks}
}

// DocSize is the length of the serialized document, which Jira limits.
func DocSize(d Doc) int {
	b, _ := json.Marshal(d)
	return len(b)
}

// Text is a text run. Empty text is not allowed by ADF; callers skip it.
func Text(s string, marks ...Node) Node {
	n := Node{"type": "text", "text": s}
	if len(marks) > 0 {
		n["marks"] = marks
	}
	return n
}

// Strong, Code and Link are text marks.
func Strong() Node { return Node{"type": "strong"} }
func Code() Node   { return Node{"type": "code"} }
func Link(href string) Node {
	return Node{"type": "link", "attrs": map[string]any{"href": href}}
}

// Paragraph holds inline nodes.
func Paragraph(inline ...Node) Node {
	if inline == nil {
		inline = []Node{}
	}
	return Node{"type": "paragraph", "content": inline}
}

// Heading is a level 1-6 heading.
func Heading(level int, inline ...Node) Node {
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	return Node{"type": "heading", "attrs": map[string]any{"level": level}, "content": inline}
}

// CodeBlock is a preformatted block; language may be empty.
func CodeBlock(language, text string) Node {
	n := Node{"type": "codeBlock", "content": []Node{}}
	if language != "" {
		n["attrs"] = map[string]any{"language": language}
	}
	if text != "" {
		n["content"] = []Node{Text(text)}
	}
	return n
}

// Panel is a coloured callout: info, note, warning, success or error.
func Panel(kind string, blocks ...Node) Node {
	return Node{"type": "panel", "attrs": map[string]any{"panelType": kind}, "content": blocks}
}

// Rule is a horizontal divider.
func Rule() Node { return Node{"type": "rule"} }

// ListItem wraps block content of one list entry.
func ListItem(blocks ...Node) Node { return Node{"type": "listItem", "content": blocks} }

// BulletList and OrderedList hold list items.
func BulletList(items ...Node) Node  { return Node{"type": "bulletList", "content": items} }
func OrderedList(items ...Node) Node { return Node{"type": "orderedList", "content": items} }

// Table builds a table whose first row is a header row.
func Table(header []string, rows [][]Node) Node {
	cell := func(kind string, inline []Node) Node {
		return Node{"type": kind, "content": []Node{Paragraph(inline...)}}
	}
	var trs []Node
	if len(header) > 0 {
		var hs []Node
		for _, h := range header {
			hs = append(hs, cell("tableHeader", Inline(h)))
		}
		trs = append(trs, Node{"type": "tableRow", "content": hs})
	}
	for _, r := range rows {
		var cs []Node
		for _, c := range r {
			cs = append(cs, Node{"type": "tableCell", "content": []Node{c}})
		}
		trs = append(trs, Node{"type": "tableRow", "content": cs})
	}
	return Node{"type": "table", "attrs": map[string]any{"isNumberColumnEnabled": false, "layout": "default"}, "content": trs}
}

var (
	inlineRe   = regexp.MustCompile("`([^`]+)`|\\[([^\\]]+)\\]\\((https?://[^)\\s]+)\\)|\\*\\*(.+?)\\*\\*|__(.+?)__|(https?://[^\\s<>()]+[^\\s<>().,;:!?'\"])")
	headingRe  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	orderedRe  = regexp.MustCompile(`^\d+[.)]\s+(.*)$`)
	bulletRe   = regexp.MustCompile(`^[-*+]\s+(.*)$`)
	tableSepRe = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?$`)
)

// Inline converts a line of Markdown inline syntax (code spans, links, bold and
// bare URLs) to text nodes. Anything else stays literal text.
func Inline(s string) []Node {
	var out []Node
	add := func(n Node) {
		if t, _ := n["text"].(string); t != "" {
			out = append(out, n)
		}
	}
	for s != "" {
		loc := inlineRe.FindStringSubmatchIndex(s)
		if loc == nil {
			add(Text(s))
			break
		}
		add(Text(s[:loc[0]]))
		group := func(i int) string {
			if loc[2*i] < 0 {
				return ""
			}
			return s[loc[2*i]:loc[2*i+1]]
		}
		switch {
		case group(1) != "":
			add(Text(group(1), Code()))
		case group(2) != "":
			add(Text(group(2), Link(group(3))))
		case group(4) != "":
			add(Text(group(4), Strong()))
		case group(5) != "":
			add(Text(group(5), Strong()))
		case group(6) != "":
			add(Text(group(6), Link(group(6))))
		}
		s = s[loc[1]:]
	}
	if out == nil {
		out = []Node{}
	}
	return out
}

func cells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// FromMarkdown converts the Markdown subset agents write (headings, paragraphs,
// lists, quotes, rules, fenced code and pipe tables) to ADF blocks. Unsupported
// syntax is kept as text, never dropped. minHeading shifts heading levels so a
// document embedded under a section heading nests below it.
func FromMarkdown(src string, minHeading int) []Node {
	var (
		out                 []Node
		para                []string
		list                []Node
		listOrdered, inCode bool
		code, table         []string
		codeLang            string
	)
	flushPara := func() {
		if len(para) > 0 {
			out = append(out, Paragraph(Inline(strings.Join(para, " "))...))
			para = nil
		}
	}
	flushList := func() {
		if len(list) > 0 {
			if listOrdered {
				out = append(out, OrderedList(list...))
			} else {
				out = append(out, BulletList(list...))
			}
			list = nil
		}
	}
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		head := cells(table[0])
		var rows [][]Node
		for _, l := range table[1:] {
			if tableSepRe.MatchString(strings.TrimSpace(l)) {
				continue
			}
			var r []Node
			cs := cells(l)
			for i := range head {
				v := ""
				if i < len(cs) {
					v = cs[i]
				}
				r = append(r, Paragraph(Inline(v)...))
			}
			rows = append(rows, r)
		}
		out = append(out, Table(head, rows))
		table = nil
	}
	flushAll := func() { flushPara(); flushList(); flushTable() }
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if inCode {
				out = append(out, CodeBlock(codeLang, strings.Join(code, "\n")))
				code, inCode = nil, false
			} else {
				flushAll()
				inCode, codeLang = true, strings.TrimSpace(strings.Trim(t, "`~"))
			}
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		if strings.HasPrefix(t, "|") {
			flushPara()
			flushList()
			table = append(table, t)
			continue
		}
		flushTable()
		switch {
		case t == "":
			flushPara()
			flushList()
		case headingRe.MatchString(t):
			flushAll()
			m := headingRe.FindStringSubmatch(t)
			level := len(m[1])
			if level < minHeading {
				level = minHeading
			}
			out = append(out, Heading(level, Inline(m[2])...))
		case t == "---" || t == "***" || t == "___":
			flushAll()
			out = append(out, Rule())
		case bulletRe.MatchString(t), orderedRe.MatchString(t):
			flushPara()
			ordered := orderedRe.MatchString(t) && !bulletRe.MatchString(t)
			if len(list) > 0 && ordered != listOrdered {
				flushList()
			}
			listOrdered = ordered
			var item string
			if ordered {
				item = orderedRe.FindStringSubmatch(t)[1]
			} else {
				item = bulletRe.FindStringSubmatch(t)[1]
			}
			list = append(list, ListItem(Paragraph(Inline(item)...)))
		case strings.HasPrefix(t, ">"):
			flushAll()
			out = append(out, Node{"type": "blockquote", "content": []Node{Paragraph(Inline(strings.TrimSpace(strings.TrimLeft(t, ">")))...)}})
		default:
			flushList()
			para = append(para, t)
		}
	}
	if inCode {
		out = append(out, CodeBlock(codeLang, strings.Join(code, "\n")))
	}
	flushAll()
	return out
}
