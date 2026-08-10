package docx

import (
	"encoding/xml"
	"strings"
)

// Namespace URIs used to disambiguate WordprocessingML runs/text from OMML
// (Office Math Markup Language) elements, which share local names like "r" and
// "t". Go's encoding/xml resolves prefixes to these URIs when the enclosing
// xmlns declarations are in scope.
const (
	// Transitional OOXML namespaces (the common case for 3GPP DOCX).
	wNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	mNS = "http://schemas.openxmlformats.org/officeDocument/2006/math"
	// Strict OOXML namespaces (e.g. TS 22.839).
	wNSStrict = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	mNSStrict = "http://purl.oclc.org/ooxml/officeDocument/math"
)

// isWordNS reports whether an element belongs to WordprocessingML (transitional
// or strict OOXML). An empty namespace is treated as Word for backward
// compatibility with raw-byte test fixtures that omit xmlns declarations.
func isWordNS(space string) bool {
	return space == "" || space == wNS || space == wNSStrict
}

// isMathNS reports whether an element belongs to OMML (transitional or strict).
func isMathNS(space string) bool {
	return space == mNS || space == mNSStrict
}

// ommlNode is a lightweight in-memory representation of an OMML element,
// keyed by local name. Attributes are keyed by local name (mirroring
// getAttrVal semantics), so m:val is read as Attr["val"].
type ommlNode struct {
	Local    string
	Attr     map[string]string
	Text     string
	Children []*ommlNode
}

// maxOMMLDepth bounds parseOMMLNode's recursion. Real formulas nest a few
// dozen levels at most; anything deeper is corrupt or hostile input that
// would otherwise exhaust the stack.
const maxOMMLDepth = 100

// ommlToLaTeX consumes the m:oMath / m:oMathPara subtree from d (the start
// element has already been consumed by the caller) and returns a LaTeX string
// without surrounding "$" delimiters.
func ommlToLaTeX(d *xml.Decoder, start xml.StartElement) string {
	root := parseOMMLNode(d, start, 0)
	return trimMathSpace(escapeMathAngles(renderOMML(root)))
}

// angleReplacer rewrites the angle brackets escapeMathAngles removes. \lt and
// \gt are defined by both KaTeX and MathJax; the trailing space keeps them
// from running into a following letter, and trimMathSpace drops it at the
// edges.
var angleReplacer = strings.NewReplacer("<", "\\lt ", ">", "\\gt ")

// escapeMathAngles enforces the invariant that rendered LaTeX never contains
// "<" or ">". Table cells embed math in raw HTML without escaping it (see
// writeParagraphInline in table.go), so an angle bracket there would open a
// bogus tag and the web viewer's sanitizer would silently eat the rest of the
// formula. Applying it once to the finished formula covers every source of a
// bracket — literal text, an unmapped n-ary or accent character — rather than
// leaving each render path to remember. Delimiters are the exception and are
// mapped by latexDelim before this runs, because "\left" needs "\langle", not
// the relation "\lt".
//
// "&" needs no such treatment: in HTML text content a lone ampersand is inert,
// and every "&" this package emits is either a matrix column separator or an
// escaped "\&".
func escapeMathAngles(s string) string {
	if !strings.ContainsAny(s, "<>") {
		return s
	}
	return angleReplacer.Replace(s)
}

// parseOMMLNode builds the subtree rooted at start, reading tokens from d until
// the matching end element. It recurses into every child (including unknown
// elements) so the decoder is always balanced regardless of the OMML content.
// Children beyond maxOMMLDepth are consumed (keeping the decoder balanced) but
// not retained.
func parseOMMLNode(d *xml.Decoder, start xml.StartElement, depth int) *ommlNode {
	n := &ommlNode{Local: start.Name.Local}
	for _, a := range start.Attr {
		if n.Attr == nil {
			n.Attr = make(map[string]string)
		}
		n.Attr[a.Name.Local] = a.Value
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return n
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth >= maxOMMLDepth {
				_ = d.Skip()
				continue
			}
			n.Children = append(n.Children, parseOMMLNode(d, t, depth+1))
		case xml.CharData:
			n.Text += string(t)
		case xml.EndElement:
			return n
		}
	}
}

// child returns the first child with the given local name, or nil.
func child(n *ommlNode, local string) *ommlNode {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Local == local {
			return c
		}
	}
	return nil
}

// children returns all children with the given local name.
func children(n *ommlNode, local string) []*ommlNode {
	if n == nil {
		return nil
	}
	var out []*ommlNode
	for _, c := range n.Children {
		if c.Local == local {
			out = append(out, c)
		}
	}
	return out
}

// mVal reads the m:val attribute of the named child of prNode (e.g. the
// begChr of a dPr). ok is false when the child or attribute is absent.
func mVal(prNode *ommlNode, childLocal string) (val string, ok bool) {
	c := child(prNode, childLocal)
	if c == nil {
		return "", false
	}
	v, ok := c.Attr["val"]
	return v, ok
}

// isTrue reports whether an OMML boolean attribute value is set. OMML on/off
// values are "1"/"true"/"on" for true; an absent value defaults to true.
func isTrue(v string, present bool) bool {
	if !present {
		return false
	}
	switch strings.ToLower(v) {
	case "0", "false", "off":
		return false
	default:
		return true
	}
}

// renderOMML converts an OMML node to LaTeX, dispatching on its local name.
func renderOMML(n *ommlNode) string {
	if n == nil {
		return ""
	}
	// Property nodes (rPr, mPr, naryPr, ...) carry only formatting attributes
	// and never contribute rendered content; their attributes are read
	// explicitly by the handlers that need them.
	if strings.HasSuffix(n.Local, "Pr") {
		return ""
	}

	switch n.Local {
	case "t":
		return escapeMathText(n.Text)
	case "oMathPara":
		return renderMathPara(n)
	case "f":
		return renderFraction(n)
	case "d":
		return renderDelimiter(n)
	case "sSub":
		return "{" + renderOMML(child(n, "e")) + "}_{" + renderOMML(child(n, "sub")) + "}"
	case "sSup":
		return "{" + renderOMML(child(n, "e")) + "}^{" + renderOMML(child(n, "sup")) + "}"
	case "sSubSup":
		return "{" + renderOMML(child(n, "e")) + "}_{" + renderOMML(child(n, "sub")) +
			"}^{" + renderOMML(child(n, "sup")) + "}"
	case "rad":
		return renderRadical(n)
	case "nary":
		return renderNary(n)
	case "m":
		return renderMatrix(n)
	case "mr":
		return joinCells(n)
	case "func":
		return renderFunc(n)
	case "acc":
		return renderAccent(n)
	default:
		// Transparent containers (oMath, e, num, den, deg, sub, sup, r, ...)
		// and unrecognized elements: recurse into children so their text
		// still surfaces.
		return renderChildren(n)
	}
}

// renderChildren concatenates the LaTeX of all child nodes.
func renderChildren(n *ommlNode) string {
	var b strings.Builder
	for _, c := range n.Children {
		b.WriteString(renderOMML(c))
	}
	return b.String()
}

// joinCells renders a matrix row's cells (m:e children) joined with " & ".
func joinCells(row *ommlNode) string {
	cells := children(row, "e")
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = renderOMML(c)
	}
	return strings.Join(parts, " & ")
}

func renderFraction(n *ommlNode) string {
	num := renderOMML(child(n, "num"))
	den := renderOMML(child(n, "den"))
	if typ, ok := mVal(child(n, "fPr"), "type"); ok {
		switch typ {
		case "lin":
			return linOperand(num) + "/" + linOperand(den)
		case "noBar":
			return "{" + num + " \\atop " + den + "}"
		}
	}
	return "\\frac{" + num + "}{" + den + "}"
}

// linOperand fences a linear-fraction operand that would otherwise lose its
// grouping. m:num and m:den are separate operands, but "/" is a plain character
// in LaTeX, so splicing them together turns "(a+b)/(c+d)" into "a+b/c+d", which
// reads as a+(b/c)+d — a silent change of meaning (issue #141).
func linOperand(s string) string {
	s = trimMathSpace(s)
	if !needsLinGroup(s) {
		return s
	}
	return "\\left(" + s + "\\right)"
}

// looseMathCmds are the binary operators and relations escapeMathText emits as
// LaTeX commands. They bind no more tightly than "/", so needsLinGroup has to
// spot them just like a literal "+": "a±b" reaches it as "a\pm b", with the
// operator hidden inside a control word.
var looseMathCmds = map[string]bool{
	// Additive-level operators.
	"pm": true, "mp": true, "cup": true, "cap": true,
	"oplus": true, "otimes": true,
	// Multiplicative operators. They re-associate in a denominator —
	// "a/(b×c)" flattened to "a/b\times c" reads as (a/b)×c.
	"times": true, "div": true, "cdot": true, "circ": true,
	// Relations.
	"leq": true, "geq": true, "neq": true, "approx": true, "equiv": true,
	"in": true, "notin": true, "subset": true, "subseteq": true,
	"propto": true, "rightarrow": true, "leftarrow": true,
	"leftrightarrow": true, "Rightarrow": true, "Leftrightarrow": true,
}

// needsLinGroup reports whether s binds loosely enough that "/" would steal
// part of it. A top-level operator or relation makes it so; everything inside
// braces or a \left...\right pair is already grouped, and a leading sign is
// unary. Juxtaposition stays unfenced: "2n" is indistinguishable from a single
// atom here, so "a/2n" keeps the source's own ambiguity.
func needsLinGroup(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case '\\':
			name := latexCmdName(s[i+1:])
			switch {
			case name == "left":
				depth++
			case name == "right":
				if depth > 0 {
					depth--
				}
			case depth == 0 && i > 0 && looseMathCmds[name]:
				return true
			}
			// Step over the control word, or over the single escaped
			// character of a control symbol such as "\-", so neither the
			// command spelling nor the escaped character is read as an
			// operator.
			i += max(len(name), 1)
		// "*" covers both the ASCII character and U+2217, which mathSymbols
		// maps to it; like \times it re-associates in a denominator.
		case '+', '-', '*', '/', '=', '<', '>':
			if depth == 0 && i > 0 {
				return true
			}
		}
	}
	return false
}

// latexCmdName returns the control word starting at the beginning of s (the
// letters that follow a backslash), or "" when the backslash introduces a
// control symbol such as "\{".
func latexCmdName(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return s[:i]
		}
	}
	return s
}

func renderDelimiter(n *ommlNode) string {
	dPr := child(n, "dPr")
	// OMML defaults: begChr "(", endChr ")", sepChr "|". The separator only
	// matters when a single delimiter wraps multiple <m:e> children.
	beg, sep, end := "(", "|", ")"
	if v, ok := mVal(dPr, "begChr"); ok {
		beg = v
	}
	if v, ok := mVal(dPr, "endChr"); ok {
		end = v
	}
	if v, ok := mVal(dPr, "sepChr"); ok {
		sep = v
	}
	elems := children(n, "e")
	parts := make([]string, len(elems))
	for i, e := range elems {
		parts[i] = renderOMML(e)
	}
	joiner := ""
	if sep != "" {
		joiner = latexDelim(sep)
	}
	return "\\left" + latexDelim(beg) + strings.Join(parts, joiner) + "\\right" + latexDelim(end)
}

func renderRadical(n *ommlNode) string {
	e := renderOMML(child(n, "e"))
	degHide, present := mVal(child(n, "radPr"), "degHide")
	deg := renderOMML(child(n, "deg"))
	if isTrue(degHide, present) || trimMathSpace(deg) == "" {
		return "\\sqrt{" + e + "}"
	}
	return "\\sqrt[" + deg + "]{" + e + "}"
}

func renderNary(n *ommlNode) string {
	naryPr := child(n, "naryPr")
	chr, ok := mVal(naryPr, "chr")
	op := "\\int"
	if ok {
		op = naryOp(chr)
	}
	subHide, sp := mVal(naryPr, "subHide")
	supHide, pp := mVal(naryPr, "supHide")

	var b strings.Builder
	b.WriteString(op)
	limits := false
	if sub := child(n, "sub"); sub != nil && !isTrue(subHide, sp) {
		b.WriteString("_{" + renderOMML(sub) + "}")
		limits = true
	}
	if sup := child(n, "sup"); sup != nil && !isTrue(supHide, pp) {
		b.WriteString("^{" + renderOMML(sup) + "}")
		limits = true
	}
	// With no limits to close the operator, a command runs straight into the
	// operand and forms an invalid token ("\int" + "x" must not become
	// "\intx"). Only commands need the separator; a literal operator
	// character from naryOp's default branch does not.
	if !limits && strings.HasPrefix(op, "\\") {
		b.WriteString(" ")
	}
	b.WriteString(renderOMML(child(n, "e")))
	return b.String()
}

// renderMathPara renders an m:oMathPara, which groups one or more m:oMath
// siblings that each represent a separate equation stacked in the same math
// paragraph (e.g. a set of related formulas listed together). A single child
// renders as-is; multiple children are wrapped in a "gathered" environment so
// each equation lands on its own line instead of concatenating into one run
// of LaTeX (see issue #53).
func renderMathPara(n *ommlNode) string {
	lines := children(n, "oMath")
	if len(lines) <= 1 {
		return renderChildren(n)
	}
	parts := make([]string, len(lines))
	for i, c := range lines {
		parts[i] = renderOMML(c)
	}
	return "\\begin{gathered} " + strings.Join(parts, " \\\\ ") + " \\end{gathered}"
}

func renderMatrix(n *ommlNode) string {
	rows := children(n, "mr")
	parts := make([]string, len(rows))
	for i, r := range rows {
		parts[i] = joinCells(r)
	}
	return "\\begin{matrix} " + strings.Join(parts, " \\\\ ") + " \\end{matrix}"
}

func renderFunc(n *ommlNode) string {
	name := trimMathSpace(renderOMML(child(n, "fName")))
	arg := renderOMML(child(n, "e"))
	var op string
	switch name {
	case "sin", "cos", "tan", "cot", "sec", "csc",
		"sinh", "cosh", "tanh", "log", "ln", "exp",
		"lim", "max", "min", "det", "gcd", "arg", "deg":
		op = "\\" + name
	case "":
		op = ""
	default:
		op = "\\operatorname{" + name + "}"
	}
	if op == "" {
		return arg
	}
	// Separate the command from its argument so a bare identifier does not
	// form an invalid token (e.g. "\sin" + "x" must not become "\sinx").
	return op + " " + arg
}

func renderAccent(n *ommlNode) string {
	chr, ok := mVal(child(n, "accPr"), "chr")
	acc := "\\bar"
	if ok {
		acc = accentCmd(chr)
	}
	return acc + "{" + renderOMML(child(n, "e")) + "}"
}

// latexDelim maps an OMML delimiter character to its LaTeX form for use after
// \left and \right. An empty delimiter becomes "." (no visible fence).
func latexDelim(chr string) string {
	switch chr {
	case "":
		return "."
	case "{":
		return "\\{"
	case "}":
		return "\\}"
	case "|":
		return "|"
	case "‖":
		return "\\|"
	case "⟨", "〈", "<":
		return "\\langle "
	case "⟩", "〉", ">":
		return "\\rangle "
	case "⌊":
		return "\\lfloor "
	case "⌋":
		return "\\rfloor "
	case "⌈":
		return "\\lceil "
	case "⌉":
		return "\\rceil "
	default:
		return chr
	}
}

// naryOp maps an OMML n-ary operator character to its LaTeX command.
func naryOp(chr string) string {
	switch chr {
	case "∑":
		return "\\sum"
	case "∏":
		return "\\prod"
	case "∐":
		return "\\coprod"
	case "∫":
		return "\\int"
	case "∬":
		return "\\iint"
	case "∭":
		return "\\iiint"
	case "∮":
		return "\\oint"
	case "⋃":
		return "\\bigcup"
	case "⋂":
		return "\\bigcap"
	case "⋁":
		return "\\bigvee"
	case "⋀":
		return "\\bigwedge"
	case "⨁":
		return "\\bigoplus"
	case "⨀":
		return "\\bigodot"
	case "⨂":
		return "\\bigotimes"
	default:
		return chr
	}
}

// accentCmd maps an OMML accent character to its LaTeX command.
func accentCmd(chr string) string {
	switch chr {
	case "̂", "^":
		return "\\hat"
	case "̃", "~":
		return "\\tilde"
	case "̄", "‾", "¯":
		return "\\bar"
	case "⃗", "→":
		return "\\vec"
	case "̇", "˙":
		return "\\dot"
	case "̈", "¨":
		return "\\ddot"
	default:
		return "\\bar"
	}
}

// mathSymbols maps common Unicode math characters to their LaTeX commands.
// Values carry a trailing space so the command separates from a following
// letter. Unmapped runes pass through unchanged.
var mathSymbols = map[rune]string{
	// Greek lowercase
	'α': "\\alpha ", 'β': "\\beta ", 'γ': "\\gamma ", 'δ': "\\delta ",
	'ε': "\\epsilon ", 'ζ': "\\zeta ", 'η': "\\eta ", 'θ': "\\theta ",
	'ι': "\\iota ", 'κ': "\\kappa ", 'λ': "\\lambda ", 'μ': "\\mu ",
	'ν': "\\nu ", 'ξ': "\\xi ", 'π': "\\pi ", 'ρ': "\\rho ",
	'σ': "\\sigma ", 'τ': "\\tau ", 'υ': "\\upsilon ", 'φ': "\\phi ",
	'χ': "\\chi ", 'ψ': "\\psi ", 'ω': "\\omega ", 'ϕ': "\\phi ",
	// Greek uppercase
	'Γ': "\\Gamma ", 'Δ': "\\Delta ", 'Θ': "\\Theta ", 'Λ': "\\Lambda ",
	'Ξ': "\\Xi ", 'Π': "\\Pi ", 'Σ': "\\Sigma ", 'Φ': "\\Phi ",
	'Ψ': "\\Psi ", 'Ω': "\\Omega ",
	// Operators and relations
	'≤': "\\leq ", '≥': "\\geq ", '≠': "\\neq ", '≈': "\\approx ",
	'≡': "\\equiv ", '×': "\\times ", '÷': "\\div ", '±': "\\pm ",
	'∓': "\\mp ", '⋅': "\\cdot ", '·': "\\cdot ", '∞': "\\infty ",
	'∈': "\\in ", '∉': "\\notin ", '⊆': "\\subseteq ", '⊂': "\\subset ",
	'∀': "\\forall ", '∃': "\\exists ", '∇': "\\nabla ", '∂': "\\partial ",
	'→': "\\rightarrow ", '←': "\\leftarrow ", '⇒': "\\Rightarrow ",
	'⇔': "\\Leftrightarrow ", '↔': "\\leftrightarrow ", '∝': "\\propto ",
	'∪': "\\cup ", '∩': "\\cap ", '∅': "\\emptyset ", '√': "\\surd ",
	'∗': "*", '∘': "\\circ ", '⊕': "\\oplus ", '⊗': "\\otimes ",
	'‖': "\\| ", '′': "'", '″': "''", '…': "\\dots ", '⋯': "\\cdots ",
	'−': "-", // U+2212 minus sign → ASCII hyphen
}

// mathSpace is the protected form of a literal space inside m:t. Math mode
// discards ordinary spaces ("n mod 2" would render as "nmod2"), so a space has
// to be re-emitted as a text-mode group to survive rendering (issue #140).
// \text{ } is used rather than "\ " because it also survives whitespace
// trimming: TrimSpace would strip the space off "\ " and leave a stray
// backslash behind.
const mathSpace = "\\text{ }"

// trimMathSpace trims whitespace and protected spaces from both ends of
// rendered math. Math that is nothing but spacing therefore still compares
// equal to "", and a padded function name still matches the operator table.
func trimMathSpace(s string) string {
	for prev := ""; s != prev; {
		prev = s
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, mathSpace)
		s = strings.TrimSuffix(s, mathSpace)
	}
	return s
}

// escapeMathText escapes LaTeX-special literals in ordinary math text and maps
// common Unicode symbols to LaTeX commands.
func escapeMathText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if cmd, ok := mathSymbols[r]; ok {
			b.WriteString(cmd)
			continue
		}
		switch r {
		case '\\':
			b.WriteString("\\backslash ")
		case '%':
			b.WriteString("\\%")
		case '#':
			b.WriteString("\\#")
		case '&':
			b.WriteString("\\&")
		case '_':
			b.WriteString("\\_")
		case '{':
			b.WriteString("\\{")
		case '}':
			b.WriteString("\\}")
		case '$':
			b.WriteString("\\$")
		case '~':
			// \textasciitilde and \textasciicircum are text-mode commands,
			// and every caller wraps this output in $...$, so they have to
			// go back through \text{} to stay defined in math mode.
			b.WriteString("\\text{\\textasciitilde}")
		case '^':
			b.WriteString("\\text{\\textasciicircum}")
		case ' ':
			b.WriteString(mathSpace)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
