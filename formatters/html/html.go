package html

import (
	"fmt"
	"html"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
)

// Option sets an option of the HTML formatter.
type Option func(f *Formatter)

// Standalone configures the HTML formatter for generating a standalone HTML document.
func Standalone(b bool) Option { return func(f *Formatter) { f.standalone = b } }

// ClassPrefix sets the CSS class prefix.
func ClassPrefix(prefix string) Option { return func(f *Formatter) { f.prefix = prefix } }

// WithClasses emits HTML using CSS classes, rather than inline styles.
func WithClasses(b bool) Option { return func(f *Formatter) { f.Classes = b } }

// WithAllClasses disables an optimisation that omits redundant CSS classes.
func WithAllClasses(b bool) Option { return func(f *Formatter) { f.allClasses = b } }

// WithCustomCSS sets user's custom CSS styles.
func WithCustomCSS(css map[chroma.TokenType]string) Option {
	return func(f *Formatter) {
		f.customCSS = css
	}
}

// WithCSSComments adds prefixe comments to the css classes. Defaults to true.
func WithCSSComments(b bool) Option { return func(f *Formatter) { f.writeCSSComments = b } }

// TabWidth sets the number of characters for a tab. Defaults to 8.
func TabWidth(width int) Option { return func(f *Formatter) { f.tabWidth = width } }

// PreventSurroundingPre prevents the surrounding pre tags around the generated code.
func PreventSurroundingPre(b bool) Option {
	return func(f *Formatter) {
		f.preventSurroundingPre = b

		if b {
			f.preWrapper = nopPreWrapper
		} else {
			f.preWrapper = defaultPreWrapper
		}
	}
}

// InlineCode creates inline code wrapped in a code tag.
func InlineCode(b bool) Option {
	return func(f *Formatter) {
		f.inlineCode = b
		f.preWrapper = preWrapper{
			start: func(code bool, styleAttr string) string {
				if code {
					return fmt.Sprintf(`<code%s>`, styleAttr)
				}

				return ``
			},
			end: func(code bool) string {
				if code {
					return `</code>`
				}

				return ``
			},
		}
	}
}

// WithPreWrapper allows control of the surrounding pre tags.
func WithPreWrapper(wrapper PreWrapper) Option {
	return func(f *Formatter) {
		f.preWrapper = wrapper
	}
}

// WrapLongLines wraps long lines.
func WrapLongLines(b bool) Option {
	return func(f *Formatter) {
		f.wrapLongLines = b
	}
}

// WithLineNumbers formats output with line numbers.
func WithLineNumbers(b bool) Option {
	return func(f *Formatter) {
		f.lineNumbers = b
	}
}

// LineNumbersInTable will, when combined with WithLineNumbers, separate the line numbers
// and code in table td's, which make them copy-and-paste friendly.
func LineNumbersInTable(b bool) Option {
	return func(f *Formatter) {
		f.lineNumbersInTable = b
	}
}

// WithLinkableLineNumbers decorates the line numbers HTML elements with an "id"
// attribute so they can be linked.
func WithLinkableLineNumbers(b bool, prefix string) Option {
	return func(f *Formatter) {
		f.linkableLineNumbers = b
		f.lineNumbersIDPrefix = prefix
	}
}

// HighlightLines higlights the given line ranges with the Highlight style.
//
// A range is the beginning and ending of a range as 1-based line numbers, inclusive.
func HighlightLines(ranges [][2]int) Option {
	return func(f *Formatter) {
		f.highlightRanges = ranges
		sort.Sort(f.highlightRanges)
	}
}

// BaseLineNumber sets the initial number to start line numbering at. Defaults to 1.
func BaseLineNumber(n int) Option {
	return func(f *Formatter) {
		f.baseLineNumber = n
	}
}

// New HTML formatter.
func New(options ...Option) *Formatter {
	f := &Formatter{
		baseLineNumber:   1,
		preWrapper:       defaultPreWrapper,
		writeCSSComments: true,
	}
	f.styleCache = newStyleCache(f)
	for _, option := range options {
		option(f)
	}
	return f
}

// PreWrapper defines the operations supported in WithPreWrapper.
type PreWrapper interface {
	// Start is called to write a start <pre> element.
	// The code flag tells whether this block surrounds
	// highlighted code. This will be false when surrounding
	// line numbers.
	Start(code bool, styleAttr string) string

	// End is called to write the end </pre> element.
	End(code bool) string
}

type preWrapper struct {
	start func(code bool, styleAttr string) string
	end   func(code bool) string
}

func (p preWrapper) Start(code bool, styleAttr string) string {
	return p.start(code, styleAttr)
}

func (p preWrapper) End(code bool) string {
	return p.end(code)
}

var (
	nopPreWrapper = preWrapper{
		start: func(code bool, styleAttr string) string { return "" },
		end:   func(code bool) string { return "" },
	}
	defaultPreWrapper = preWrapper{
		start: func(code bool, styleAttr string) string {
			if code {
				return fmt.Sprintf(`<pre%s><code>`, styleAttr)
			}

			return fmt.Sprintf(`<pre%s>`, styleAttr)
		},
		end: func(code bool) string {
			if code {
				return `</code></pre>`
			}

			return `</pre>`
		},
	}
)

// Formatter that generates HTML.
type Formatter struct {
	styleCache            *styleCache
	standalone            bool
	prefix                string
	Classes               bool // Exported field to detect when classes are being used
	allClasses            bool
	customCSS             map[chroma.TokenType]string
	writeCSSComments      bool
	preWrapper            PreWrapper
	inlineCode            bool
	preventSurroundingPre bool
	tabWidth              int
	wrapLongLines         bool
	lineNumbers           bool
	lineNumbersInTable    bool
	linkableLineNumbers   bool
	lineNumbersIDPrefix   string
	highlightRanges       highlightRanges
	baseLineNumber        int
}

type highlightRanges [][2]int

func (h highlightRanges) Len() int           { return len(h) }
func (h highlightRanges) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h highlightRanges) Less(i, j int) bool { return h[i][0] < h[j][0] }

func (f *Formatter) Format(w io.Writer, style *chroma.Style, iterator chroma.Iterator) (err error) {
	return f.writeHTML(w, style, iterator.Tokens())
}

// We deliberately don't use html/template here because it is two orders of magnitude slower (benchmarked).
//
// OTOH we need to be super careful about correct escaping...
func (f *Formatter) writeHTML(w io.Writer, style *chroma.Style, tokens []chroma.Token) (err error) { // nolint: gocyclo
	css := f.styleCache.get(style, true)
	if f.standalone {
		fmt.Fprint(w, "<html>\n")
		if f.Classes {
			fmt.Fprint(w, "<style type=\"text/css\">\n")
			err = f.WriteCSS(w, style)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "body { %s; }\n", css[chroma.Background])
			fmt.Fprint(w, "</style>")
		}
		fmt.Fprintf(w, "<body%s>\n", f.styleAttr(css, chroma.Background))
	}

	wrapInTable := f.lineNumbers && f.lineNumbersInTable

	lines := chroma.SplitTokensIntoLines(tokens)
	lineDigits := len(strconv.Itoa(f.baseLineNumber + len(lines) - 1))
	highlightIndex := 0

	if wrapInTable {
		// List line numbers in its own <td>
		fmt.Fprintf(w, "<div%s>\n", f.styleAttr(css, chroma.PreWrapper))
		fmt.Fprintf(w, "<table%s><tr>", f.styleAttr(css, chroma.LineTable))
		fmt.Fprintf(w, "<td%s>\n", f.styleAttr(css, chroma.LineTableTD))
		fmt.Fprintf(w, "%s", f.preWrapper.Start(false, f.styleAttr(css, chroma.PreWrapper)))
		for index := range lines {
			line := f.baseLineNumber + index
			highlight, next := f.shouldHighlight(highlightIndex, line)
			if next {
				highlightIndex++
			}
			if highlight {
				fmt.Fprintf(w, "<span%s>", f.styleAttr(css, chroma.LineHighlight))
			}

			fmt.Fprintf(w, "<span%s%s>%s\n</span>", f.styleAttr(css, chroma.LineNumbersTable), f.lineIDAttribute(line), f.lineTitleWithLinkIfNeeded(css, lineDigits, line))

			if highlight {
				fmt.Fprintf(w, "</span>")
			}
		}
		fmt.Fprint(w, f.preWrapper.End(false))
		fmt.Fprint(w, "</td>\n")
		fmt.Fprintf(w, "<td%s>\n", f.styleAttr(css, chroma.LineTableTD, "width:100%"))
	}

	fmt.Fprintf(w, "%s", f.preWrapper.Start(true, f.styleAttr(css, chroma.PreWrapper)))

	highlightIndex = 0
	for index, tokens := range lines {
		// 1-based line number.
		line := f.baseLineNumber + index
		highlight, next := f.shouldHighlight(highlightIndex, line)
		if next {
			highlightIndex++
		}

		if !(f.preventSurroundingPre || f.inlineCode) {
			// Start of Line
			fmt.Fprint(w, `<span`)

			if highlight {
				// Line + LineHighlight
				if f.Classes {
					fmt.Fprintf(w, ` class="%s %s"`, f.class(chroma.Line), f.class(chroma.LineHighlight))
				} else {
					fmt.Fprintf(w, ` style="%s %s"`, css[chroma.Line], css[chroma.LineHighlight])
				}
				fmt.Fprint(w, `>`)
			} else {
				fmt.Fprintf(w, "%s>", f.styleAttr(css, chroma.Line))
			}

			// Line number
			if f.lineNumbers && !wrapInTable {
				fmt.Fprintf(w, "<span%s%s>%s</span>", f.styleAttr(css, chroma.LineNumbers), f.lineIDAttribute(line), f.lineTitleWithLinkIfNeeded(css, lineDigits, line))
			}

			fmt.Fprintf(w, `<span%s>`, f.styleAttr(css, chroma.CodeLine))
		}

		for _, token := range tokens {
			html := html.EscapeString(token.String())
			attr := f.styleAttr(css, token.Type)
			if attr != "" {
				html = fmt.Sprintf("<span%s>%s</span>", attr, html)
			}
			fmt.Fprint(w, html)
		}

		if !(f.preventSurroundingPre || f.inlineCode) {
			fmt.Fprint(w, `</span>`) // End of CodeLine

			fmt.Fprint(w, `</span>`) // End of Line
		}
	}
	fmt.Fprintf(w, "%s", f.preWrapper.End(true))

	if wrapInTable {
		fmt.Fprint(w, "</td></tr></table>\n")
		fmt.Fprint(w, "</div>\n")
	}

	if f.standalone {
		fmt.Fprint(w, "\n</body>\n")
		fmt.Fprint(w, "</html>\n")
	}

	return nil
}

func (f *Formatter) lineIDAttribute(line int) string {
	if !f.linkableLineNumbers {
		return ""
	}
	return fmt.Sprintf(" id=\"%s\"", f.lineID(line))
}

func (f *Formatter) lineTitleWithLinkIfNeeded(css map[chroma.TokenType]string, lineDigits, line int) string {
	title := fmt.Sprintf("%*d", lineDigits, line)
	if !f.linkableLineNumbers {
		return title
	}
	return fmt.Sprintf("<a%s href=\"#%s\">%s</a>", f.styleAttr(css, chroma.LineLink), f.lineID(line), title)
}

func (f *Formatter) lineID(line int) string {
	return fmt.Sprintf("%s%d", f.lineNumbersIDPrefix, line)
}

func (f *Formatter) shouldHighlight(highlightIndex, line int) (bool, bool) {
	next := false
	for highlightIndex < len(f.highlightRanges) && line > f.highlightRanges[highlightIndex][1] {
		highlightIndex++
		next = true
	}
	if highlightIndex < len(f.highlightRanges) {
		hrange := f.highlightRanges[highlightIndex]
		if line >= hrange[0] && line <= hrange[1] {
			return true, next
		}
	}
	return false, next
}

func (f *Formatter) class(t chroma.TokenType) string {
	for t != 0 {
		if cls, ok := chroma.StandardTypes[t]; ok {
			if cls != "" {
				return f.prefix + cls
			}
			return ""
		}
		t = t.Parent()
	}
	if cls := chroma.StandardTypes[t]; cls != "" {
		return f.prefix + cls
	}
	return ""
}

func (f *Formatter) styleAttr(styles map[chroma.TokenType]string, tt chroma.TokenType, extraCSS ...string) string {
	if f.Classes {
		cls := f.class(tt)
		if cls == "" {
			return ""
		}
		return fmt.Sprintf(` class="%s"`, cls)
	}
	if _, ok := styles[tt]; !ok {
		tt = tt.SubCategory()
		if _, ok := styles[tt]; !ok {
			tt = tt.Category()
			if _, ok := styles[tt]; !ok {
				return ""
			}
		}
	}
	css := []string{styles[tt]}
	css = append(css, extraCSS...)
	return fmt.Sprintf(` style="%s"`, strings.Join(css, ";"))
}

func (f *Formatter) tabWidthStyle() string {
	if f.tabWidth != 0 && f.tabWidth != 8 {
		return fmt.Sprintf("-moz-tab-size: %[1]d; -o-tab-size: %[1]d; tab-size: %[1]d;", f.tabWidth)
	}
	return ""
}

func (f *Formatter) writeCSSRule(w io.Writer, comment string, selector string, styles string) error {
	if styles == "" {
		return nil
	}
	if f.writeCSSComments && comment != "" {
		if _, err := fmt.Fprintf(w, "/* %s */ ", comment); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%s { %s }\n", selector, styles); err != nil {
		return err
	}
	return nil
}

// WriteCSS writes CSS style definitions (without any surrounding HTML).
func (f *Formatter) WriteCSS(w io.Writer, style *chroma.Style) error {
	css := f.styleCache.get(style, false)

	// Special-case background as it is mapped to the outer ".chroma" class.
	if err := f.writeCSSRule(w, chroma.Background.String(), fmt.Sprintf(".%sbg", f.prefix), css[chroma.Background]); err != nil {
		return err
	}
	// Special-case PreWrapper as it is the ".chroma" class.
	if err := f.writeCSSRule(w, chroma.PreWrapper.String(), fmt.Sprintf(".%schroma", f.prefix), css[chroma.PreWrapper]); err != nil {
		return err
	}
	// Special-case code column of table to expand width.
	if f.lineNumbers && f.lineNumbersInTable {
		selector := fmt.Sprintf(".%schroma .%s:last-child", f.prefix, f.class(chroma.LineTableTD))
		if err := f.writeCSSRule(w, chroma.LineTableTD.String(), selector, "width: 100%;"); err != nil {
			return err
		}
	}
	// Special-case line number highlighting when targeted.
	if f.lineNumbers || f.lineNumbersInTable {
		targetedLineCSS := StyleEntryToCSS(style.Get(chroma.LineHighlight))
		for _, tt := range []chroma.TokenType{chroma.LineNumbers, chroma.LineNumbersTable} {
			comment := fmt.Sprintf("%s targeted by URL anchor", tt)
			selector := fmt.Sprintf(".%schroma .%s:target", f.prefix, f.class(tt))
			if err := f.writeCSSRule(w, comment, selector, targetedLineCSS); err != nil {
				return err
			}
		}
	}
	tts := []int{}
	for tt := range css {
		tts = append(tts, int(tt))
	}
	sort.Ints(tts)
	for _, ti := range tts {
		tt := chroma.TokenType(ti)
		switch tt {
		case chroma.Background, chroma.PreWrapper:
			continue
		}
		class := f.class(tt)
		if class == "" {
			continue
		}
		if err := f.writeCSSRule(w, tt.String(), fmt.Sprintf(".%schroma .%s", f.prefix, class), css[tt]); err != nil {
			return err
		}
	}
	return nil
}

func (f *Formatter) styleToCSS(style *chroma.Style) map[chroma.TokenType]string {
	classes := map[chroma.TokenType]string{}
	bg := style.Get(chroma.Background)
	// Convert the style.
	for t := range chroma.StandardTypes {
		entry := style.Get(t)
		if t != chroma.Background {
			entry = entry.Sub(bg)
		}

		// Inherit from custom CSS provided by user
		tokenCategory := t.Category()
		tokenSubCategory := t.SubCategory()
		if t != tokenCategory {
			if css, ok := f.customCSS[tokenCategory]; ok {
				classes[t] = css
			}
		}
		if tokenCategory != tokenSubCategory {
			if css, ok := f.customCSS[tokenSubCategory]; ok {
				classes[t] += css
			}
		}
		// Add custom CSS provided by user
		if css, ok := f.customCSS[t]; ok {
			classes[t] += css
		}

		if !f.allClasses && entry.IsZero() && classes[t] == `` {
			continue
		}

		styleEntryCSS := StyleEntryToCSS(entry)
		if styleEntryCSS != `` && classes[t] != `` {
			styleEntryCSS += `;`
		}
		classes[t] = styleEntryCSS + classes[t]
	}
	classes[chroma.Background] += `;` + f.tabWidthStyle()
	classes[chroma.PreWrapper] += classes[chroma.Background]
	// Make PreWrapper a grid to show highlight style with full width.
	if len(f.highlightRanges) > 0 && f.customCSS[chroma.PreWrapper] == `` {
		classes[chroma.PreWrapper] += `display: grid;`
	}
	// Make PreWrapper wrap long lines.
	if f.wrapLongLines {
		classes[chroma.PreWrapper] += `white-space: pre-wrap; word-break: break-word;`
	}
	lineNumbersStyle := `white-space: pre; -webkit-user-select: none; user-select: none; margin-right: 0.4em; padding: 0 0.4em 0 0.4em;`
	// All rules begin with default rules followed by user provided rules
	classes[chroma.Line] = `display: flex;` + classes[chroma.Line]
	classes[chroma.LineNumbers] = lineNumbersStyle + classes[chroma.LineNumbers]
	classes[chroma.LineNumbersTable] = lineNumbersStyle + classes[chroma.LineNumbersTable]
	classes[chroma.LineTable] = "border-spacing: 0; padding: 0; margin: 0; border: 0;" + classes[chroma.LineTable]
	classes[chroma.LineTableTD] = "vertical-align: top; padding: 0; margin: 0; border: 0;" + classes[chroma.LineTableTD]
	classes[chroma.LineLink] = "outline: none; text-decoration: none; color: inherit" + classes[chroma.LineLink]
	return classes
}

// StyleEntryToCSS converts a chroma.StyleEntry to CSS attributes.
func StyleEntryToCSS(e chroma.StyleEntry) string {
	styles := []string{}
	if e.Colour.IsSet() {
		styles = append(styles, "color: "+e.Colour.String())
	}
	if e.Background.IsSet() {
		styles = append(styles, "background-color: "+e.Background.String())
	}
	if e.Bold == chroma.Yes {
		styles = append(styles, "font-weight: bold")
	}
	if e.Italic == chroma.Yes {
		styles = append(styles, "font-style: italic")
	}
	if e.Underline == chroma.Yes {
		styles = append(styles, "text-decoration: underline")
	}
	return strings.Join(styles, "; ")
}

// Compress CSS attributes - remove spaces, transform 6-digit colours to 3.
func compressStyle(s string) string {
	parts := strings.Split(s, ";")
	out := []string{}
	for _, p := range parts {
		p = strings.Join(strings.Fields(p), " ")
		p = strings.Replace(p, ": ", ":", 1)
		if strings.Contains(p, "#") {
			c := p[len(p)-6:]
			if c[0] == c[1] && c[2] == c[3] && c[4] == c[5] {
				p = p[:len(p)-6] + c[0:1] + c[2:3] + c[4:5]
			}
		}
		out = append(out, p)
	}
	return strings.Join(out, ";")
}

const styleCacheLimit = 32

type styleCacheEntry struct {
	style      *chroma.Style
	compressed bool
	cache      map[chroma.TokenType]string
}

type styleCache struct {
	mu sync.Mutex
	// LRU cache of compiled (and possibly compressed) styles. This is a slice
	// because the cache size is small, and a slice is sufficiently fast for
	// small N.
	cache []styleCacheEntry
	f     *Formatter
}

func newStyleCache(f *Formatter) *styleCache {
	return &styleCache{f: f}
}

func (l *styleCache) get(style *chroma.Style, compress bool) map[chroma.TokenType]string {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Look for an existing entry.
	for i := len(l.cache) - 1; i >= 0; i-- {
		entry := l.cache[i]
		if entry.style == style && entry.compressed == compress {
			// Top of the cache, no need to adjust the order.
			if i == len(l.cache)-1 {
				return entry.cache
			}
			// Move this entry to the end of the LRU
			copy(l.cache[i:], l.cache[i+1:])
			l.cache[len(l.cache)-1] = entry
			return entry.cache
		}
	}

	// No entry, create one.
	cached := l.f.styleToCSS(style)
	if !l.f.Classes {
		for t, style := range cached {
			cached[t] = compressStyle(style)
		}
	}
	if compress {
		for t, style := range cached {
			cached[t] = compressStyle(style)
		}
	}
	// Evict the oldest entry.
	if len(l.cache) >= styleCacheLimit {
		l.cache = l.cache[0:copy(l.cache, l.cache[1:])]
	}
	l.cache = append(l.cache, styleCacheEntry{style: style, cache: cached, compressed: compress})
	return cached
}
