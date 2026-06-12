// Package mdfmt is a dependency-free changelog formatter that reproduces the
// output of running prettier (== dprint == deno fmt) over the raw markdown
// @changesets emits, so repos with no Node or prettier still get the formatted
// changelog the Node toolchain produces.
//
// It parses the changelog into a small block model (headings, paragraphs,
// lists, fenced code, blockquotes, tables), then renders with prettier's
// rules: one blank line between block siblings; CommonMark loose/tight list
// spacing; verbatim code-fence interiors; aligned table columns;
// trailing-whitespace trim and a single final newline. Anything it does not
// recognise becomes a verbatim paragraph, so an unhandled construct is at
// worst "not reformatted", never corrupted - and table detection is strict,
// so a near-table is passed through untouched rather than mangled.
package mdfmt

import (
	"strings"
	"unicode"
)

// lineEndingReplacer mirrors .NET's string.ReplaceLineEndings("\n"), which
// recognises CRLF, CR, LF, NEL, FF, LS and PS as line terminators.
var lineEndingReplacer = strings.NewReplacer(
	"\r\n", "\n",
	"\r", "\n",
	"\u0085", "\n",
	"\u000C", "\n",
	"\u2028", "\n",
	"\u2029", "\n",
)

// Format formats markdown to match prettier's markdown output for the shapes
// that appear in changelogs. Idempotent: formatting already-formatted text
// returns it unchanged (modulo a final newline).
func Format(markdown string) string {
	// Prettier's core doc printer trims trailing whitespace at every line
	// break - even inside code fences - so it is safe to strip up front,
	// before any structural parsing.
	rawLines := strings.Split(lineEndingReplacer.Replace(markdown), "\n")
	lines := make([]string, len(rawLines))
	for i, line := range rawLines {
		lines[i] = strings.TrimRightFunc(line, unicode.IsSpace)
	}

	blocks, _ := parseBlocks(lines)
	rendered := renderBlocks(blocks, true)

	body := strings.TrimRight(strings.Join(rendered, "\n"), "\n")
	if len(body) == 0 {
		return ""
	}
	return body + "\n"
}

// ----- Block model -------------------------------------------------------

type block interface{ isBlock() }

type heading struct {
	level int
	text  string
}

type paragraph struct {
	lines []string
}

type codeFence struct {
	lines []string
}

type thematicBreak struct{}

type blockQuote struct {
	children []block
}

type listBlock struct {
	items [][]block
	loose bool
}

type align int

const (
	alignNone align = iota
	alignLeft
	alignRight
	alignCenter
)

type tableBlock struct {
	header []string
	aligns []align
	rows   [][]string
}

func (heading) isBlock()       {}
func (paragraph) isBlock()     {}
func (codeFence) isBlock()     {}
func (thematicBreak) isBlock() {}
func (blockQuote) isBlock()    {}
func (listBlock) isBlock()     {}
func (tableBlock) isBlock()    {}

// ----- Parsing -----------------------------------------------------------

// parseBlocks parses a container's (already de-indented) lines into blocks.
// internalBlank reports whether a blank line separated two blocks at THIS
// level - the signal CommonMark uses to mark the enclosing list item (and
// thus its list) loose. Blanks consumed by child parsers do not surface here,
// so the signal stays level-correct under nesting.
func parseBlocks(lines []string) (blocks []block, internalBlank bool) {
	pendingBlank := false
	i := 0

	for i < len(lines) {
		line := lines[i]

		if len(line) == 0 {
			pendingBlank = true
			i++
			continue
		}

		if len(blocks) > 0 && pendingBlank {
			internalBlank = true
		}

		pendingBlank = false

		if fence, next, ok := tryParseFence(lines, i); ok {
			blocks = append(blocks, fence)
			i = next
		} else if isHeading(line) {
			blocks = append(blocks, parseHeading(line))
			i++
		} else if table, next, ok := tryParseTable(lines, i); ok {
			blocks = append(blocks, table)
			i = next
		} else if isThematicBreak(line) {
			blocks = append(blocks, thematicBreak{})
			i++
		} else if isBlockQuote(line) {
			var quote blockQuote
			quote, i = parseBlockQuote(lines, i)
			blocks = append(blocks, quote)
		} else if _, _, ok := isBulletMarker(line); ok {
			var list listBlock
			list, i = parseList(lines, i)
			blocks = append(blocks, list)
		} else {
			var para paragraph
			para, i = parseParagraph(lines, i)
			blocks = append(blocks, para)
		}
	}

	return blocks, internalBlank
}

func parseHeading(line string) heading {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}

	return heading{level: level, text: strings.TrimSpace(line[level:])}
}

func parseParagraph(lines []string, i int) (paragraph, int) {
	var content []string
	for i < len(lines) {
		line := lines[i]
		if len(line) == 0 || startsBlock(line) {
			break
		}

		content = append(content, line)
		i++
	}

	return paragraph{lines: content}, i
}

func parseBlockQuote(lines []string, i int) (blockQuote, int) {
	var inner []string
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "> ") {
			inner = append(inner, line[2:])
		} else if line == ">" {
			inner = append(inner, "")
		} else {
			break
		}

		i++
	}

	children, _ := parseBlocks(inner)
	return blockQuote{children: children}, i
}

func parseList(lines []string, i int) (listBlock, int) {
	var items [][]block
	loose := false

	for i < len(lines) {
		line := lines[i]

		if len(line) == 0 {
			// A blank here is a between-item blank only if another marker
			// for THIS list follows it; otherwise the list has ended and the
			// blank belongs to the enclosing container.
			j := i
			for j < len(lines) && len(lines[j]) == 0 {
				j++
			}

			if j < len(lines) && indent(lines[j]) == 0 {
				if _, _, ok := isBulletMarker(lines[j]); ok {
					loose = true
					i = j
					continue
				}
			}

			break
		}

		markerWidth, firstContent, ok := isBulletMarker(line)
		if indent(line) != 0 || !ok {
			break
		}

		i++
		var content []string
		if len(firstContent) > 0 {
			content = append(content, firstContent)
		}

		content, i = collectItemContent(lines, i, markerWidth, content)

		children, itemInternalBlank := parseBlocks(content)
		if itemInternalBlank {
			loose = true
		}

		items = append(items, children)
	}

	return listBlock{items: items, loose: loose}, i
}

// collectItemContent gathers the lines that belong to the current list item:
// anything indented to the item's content column, plus blank lines that are
// followed by more such indented content. Leaves the cursor on the first line
// that does not belong (a sibling marker, a dedent, or a trailing blank),
// de-indenting kept lines.
func collectItemContent(lines []string, i int, markerWidth int, content []string) ([]string, int) {
	for i < len(lines) {
		line := lines[i]

		if len(line) == 0 {
			j := i
			for j < len(lines) && len(lines[j]) == 0 {
				j++
			}

			if j < len(lines) && indent(lines[j]) >= markerWidth {
				content = append(content, "")
				i++
				continue
			}

			break
		}

		if indent(line) >= markerWidth {
			content = append(content, line[markerWidth:])
			i++
			continue
		}

		break
	}

	return content, i
}

func tryParseFence(lines []string, i int) (codeFence, int, bool) {
	marker, length, ok := isFenceOpen(lines[i])
	if !ok {
		return codeFence{}, i, false
	}

	collected := []string{lines[i]}
	j := i + 1
	for j < len(lines) {
		collected = append(collected, lines[j])
		closes := isFenceClose(lines[j], marker, length)
		j++
		if closes {
			break
		}
	}

	return codeFence{lines: collected}, j, true
}

func tryParseTable(lines []string, i int) (tableBlock, int, bool) {
	// Strict: a header row containing a pipe, immediately followed by a valid
	// delimiter row. Anything less is left to paragraph parsing and emitted
	// verbatim.
	if !strings.Contains(lines[i], "|") {
		return tableBlock{}, i, false
	}

	if i+1 >= len(lines) {
		return tableBlock{}, i, false
	}

	aligns, ok := tryParseAlignments(lines[i+1])
	if !ok {
		return tableBlock{}, i, false
	}

	header := splitRow(lines[i])

	var rows [][]string
	j := i + 2
	for j < len(lines) && len(lines[j]) > 0 && strings.Contains(lines[j], "|") {
		rows = append(rows, splitRow(lines[j]))
		j++
	}

	return tableBlock{header: header, aligns: aligns, rows: rows}, j, true
}

// ----- Rendering ---------------------------------------------------------

// renderBlocks renders blocks separated by a single blank line when
// spaceBetween is true. At document and blockquote level that is always true;
// inside a list item it is the list's looseness, which is how a tight item
// keeps its children gap-free and a loose item spaces them out.
func renderBlocks(blocks []block, spaceBetween bool) []string {
	var output []string
	for k, b := range blocks {
		if k > 0 && spaceBetween {
			output = append(output, "")
		}

		output = append(output, renderBlock(b)...)
	}

	return output
}

func renderBlock(b block) []string {
	switch v := b.(type) {
	case heading:
		hashes := strings.Repeat("#", v.level)
		if len(v.text) == 0 {
			return []string{hashes}
		}
		return []string{hashes + " " + v.text}
	case paragraph:
		return v.lines
	case codeFence:
		return v.lines
	case thematicBreak:
		return []string{"---"}
	case blockQuote:
		return renderBlockQuote(v)
	case listBlock:
		return renderList(v)
	case tableBlock:
		return renderTable(v)
	default:
		return nil
	}
}

func renderBlockQuote(quote blockQuote) []string {
	var output []string
	for _, line := range renderBlocks(quote.children, true) {
		if len(line) == 0 {
			output = append(output, ">")
		} else {
			output = append(output, "> "+line)
		}
	}

	return output
}

func renderList(list listBlock) []string {
	var output []string

	for idx, item := range list.items {
		if idx > 0 && list.loose {
			output = append(output, "")
		}

		childLines := renderBlocks(item, list.loose)
		if len(childLines) == 0 {
			output = append(output, "-")
			continue
		}

		for li, childLine := range childLines {
			if len(childLine) == 0 {
				output = append(output, "")
			} else if li == 0 {
				output = append(output, "- "+childLine)
			} else {
				output = append(output, "  "+childLine)
			}
		}
	}

	return output
}

func renderTable(table tableBlock) []string {
	columns := len(table.aligns)
	if len(table.header) > columns {
		columns = len(table.header)
	}
	for _, row := range table.rows {
		if len(row) > columns {
			columns = len(row)
		}
	}

	// Column width = max(3, widest cell across header + body), per prettier's
	// table printer.
	widths := make([]int, columns)
	for c := 0; c < columns; c++ {
		width := stringWidth(cell(table.header, c))
		if width < 3 {
			width = 3
		}
		for _, row := range table.rows {
			if rowWidth := stringWidth(cell(row, c)); rowWidth > width {
				width = rowWidth
			}
		}
		widths[c] = width
	}

	output := []string{
		renderRow(table.header, widths, table.aligns, columns),
		renderDelimiter(widths, table.aligns, columns),
	}
	for _, row := range table.rows {
		output = append(output, renderRow(row, widths, table.aligns, columns))
	}

	return output
}

func renderRow(cells []string, widths []int, aligns []align, columns int) string {
	padded := make([]string, columns)
	for c := 0; c < columns; c++ {
		padded[c] = padCell(cell(cells, c), widths[c], alignment(aligns, c))
	}

	return "| " + strings.Join(padded, " | ") + " |"
}

func renderDelimiter(widths []int, aligns []align, columns int) string {
	cells := make([]string, columns)
	for c := 0; c < columns; c++ {
		a := alignment(aligns, c)
		dashes := []byte(strings.Repeat("-", widths[c]))
		if a == alignLeft || a == alignCenter {
			dashes[0] = ':'
		}

		if a == alignRight || a == alignCenter {
			dashes[len(dashes)-1] = ':'
		}

		cells[c] = string(dashes)
	}

	return "| " + strings.Join(cells, " | ") + " |"
}

func padCell(cellText string, width int, a align) string {
	pad := width - stringWidth(cellText)
	if pad < 0 {
		pad = 0
	}

	switch a {
	case alignRight:
		return strings.Repeat(" ", pad) + cellText
	case alignCenter:
		return strings.Repeat(" ", pad/2) + cellText + strings.Repeat(" ", pad-pad/2)
	default:
		return cellText + strings.Repeat(" ", pad)
	}
}

// ----- Line classification ----------------------------------------------

func startsBlock(line string) bool {
	if isHeading(line) {
		return true
	}
	if _, _, ok := isFenceOpen(line); ok {
		return true
	}
	if isThematicBreak(line) || isBlockQuote(line) {
		return true
	}
	_, _, ok := isBulletMarker(line)
	return ok
}

// isHeading reports an ATX heading: 1-6 '#'s at column 0 then a space or end
// of line. Headings the tool emits sit at column 0; summary text is rendered
// as indented list continuation, so a '#' inside it never reaches column 0.
func isHeading(line string) bool {
	if len(line) == 0 || line[0] != '#' {
		return false
	}

	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}

	return hashes <= 6 && (hashes == len(line) || line[hashes] == ' ')
}

func isBlockQuote(line string) bool {
	return line == ">" || strings.HasPrefix(line, "> ")
}

// isBulletMarker reports a bullet marker at column 0: '-', '*' or '+'
// followed by a space (or the line is just the marker). The content column is
// always two (marker + one space), matching prettier's normalised output.
// Ordered list markers are intentionally not recognised - they do not occur
// in changelogs and fall back to verbatim.
func isBulletMarker(line string) (markerWidth int, firstContent string, ok bool) {
	if len(line) == 0 || (line[0] != '-' && line[0] != '*' && line[0] != '+') {
		return 0, "", false
	}

	if len(line) == 1 {
		return 2, "", true
	}

	if line[1] != ' ' {
		return 0, "", false
	}

	if len(line) > 2 {
		firstContent = line[2:]
	}

	return 2, firstContent, true
}

func isThematicBreak(line string) bool {
	compact := strings.ReplaceAll(line, " ", "")
	if len(compact) < 3 {
		return false
	}

	first := compact[0]
	if first != '-' && first != '*' && first != '_' {
		return false
	}

	for k := 1; k < len(compact); k++ {
		if compact[k] != first {
			return false
		}
	}

	return true
}

func isFenceOpen(line string) (marker byte, length int, ok bool) {
	trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0, false
	}

	marker = trimmed[0]
	for length < len(trimmed) && trimmed[length] == marker {
		length++
	}

	return marker, length, length >= 3
}

func isFenceClose(line string, marker byte, openLength int) bool {
	trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
	run := 0
	for run < len(trimmed) && trimmed[run] == marker {
		run++
	}

	return run >= openLength && run == len(trimmed)
}

func indent(line string) int {
	n := 0
	for n < len(line) && line[n] == ' ' {
		n++
	}

	return n
}

// ----- Table helpers -----------------------------------------------------

func tryParseAlignments(line string) ([]align, bool) {
	if !strings.Contains(line, "|") && !strings.Contains(line, "-") {
		return nil, false
	}

	cells := splitRow(line)
	if len(cells) == 0 {
		return nil, false
	}

	parsed := make([]align, 0, len(cells))
	for _, c := range cells {
		content := strings.TrimSpace(c)
		if len(content) == 0 {
			return nil, false
		}

		left := content[0] == ':'
		right := content[len(content)-1] == ':'
		dashes := strings.Trim(content, ":")
		if len(dashes) == 0 || strings.Count(dashes, "-") != len(dashes) {
			return nil, false
		}

		switch {
		case left && right:
			parsed = append(parsed, alignCenter)
		case left:
			parsed = append(parsed, alignLeft)
		case right:
			parsed = append(parsed, alignRight)
		default:
			parsed = append(parsed, alignNone)
		}
	}

	return parsed, true
}

func splitRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")

	cells := strings.Split(trimmed, "|")
	for k := range cells {
		cells[k] = strings.TrimSpace(cells[k])
	}

	return cells
}

func cell(cells []string, column int) string {
	if column < len(cells) {
		return cells[column]
	}
	return ""
}

func alignment(aligns []align, column int) align {
	if column < len(aligns) {
		return aligns[column]
	}
	return alignNone
}

// stringWidth approximates prettier's string-width: 0 for combining marks, 2
// for East Asian wide and fullwidth code points (and emoji), 1 otherwise.
// Changelog tables are overwhelmingly ASCII, where this is exact; the
// wide-character handling keeps non-ASCII summaries from misaligning badly.
func stringWidth(text string) int {
	width := 0
	for _, r := range text {
		width += runeWidth(r)
	}

	return width
}

func runeWidth(codePoint rune) int {
	switch {
	case codePoint >= 0x0300 && codePoint <= 0x036F,
		codePoint >= 0x1AB0 && codePoint <= 0x1AFF,
		codePoint >= 0x1DC0 && codePoint <= 0x1DFF,
		codePoint >= 0x20D0 && codePoint <= 0x20FF,
		codePoint >= 0xFE20 && codePoint <= 0xFE2F:
		return 0
	case codePoint >= 0x1100 && codePoint <= 0x115F,
		codePoint >= 0x2E80 && codePoint <= 0xA4CF,
		codePoint >= 0xAC00 && codePoint <= 0xD7A3,
		codePoint >= 0xF900 && codePoint <= 0xFAFF,
		codePoint >= 0xFE30 && codePoint <= 0xFE4F,
		codePoint >= 0xFF00 && codePoint <= 0xFF60,
		codePoint >= 0xFFE0 && codePoint <= 0xFFE6,
		codePoint >= 0x1F300 && codePoint <= 0x1FAFF,
		codePoint >= 0x20000 && codePoint <= 0x3FFFD:
		return 2
	default:
		return 1
	}
}
