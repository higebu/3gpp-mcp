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

// ommlToLaTeX consumes the m:oMath / m:oMathPara subtree from d (the start
// element has already been consumed by the caller) and returns a LaTeX string
// without surrounding "$" delimiters.
func ommlToLaTeX(d *xml.Decoder, start xml.StartElement) string {
	root := parseOMMLNode(d, start)
	return strings.TrimSpace(renderOMML(root))
}

// parseOMMLNode builds the subtree rooted at start, reading tokens from d until
// the matching end element. It recurses into every child (including unknown
// elements) so the decoder is always balanced regardless of the OMML content.
func parseOMMLNode(d *xml.Decoder, start xml.StartElement) *ommlNode {
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
			n.Children = append(n.Children, parseOMMLNode(d, t))
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
		// Transparent containers (oMath, oMathPara, e, num, den, deg, sub, sup,
		// r, ...) and unrecognized elements: recurse into children so their
		// text still surfaces.
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
			return num + "/" + den
		case "noBar":
			return "{" + num + " \\atop " + den + "}"
		}
	}
	return "\\frac{" + num + "}{" + den + "}"
}

func renderDelimiter(n *ommlNode) string {
	dPr := child(n, "dPr")
	beg, sep, end := "(", "", ")"
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
	if isTrue(degHide, present) || strings.TrimSpace(deg) == "" {
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
	if sub := child(n, "sub"); sub != nil && !isTrue(subHide, sp) {
		b.WriteString("_{" + renderOMML(sub) + "}")
	}
	if sup := child(n, "sup"); sup != nil && !isTrue(supHide, pp) {
		b.WriteString("^{" + renderOMML(sup) + "}")
	}
	b.WriteString(renderOMML(child(n, "e")))
	return b.String()
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
	name := strings.TrimSpace(renderOMML(child(n, "fName")))
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
	return op + arg
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
	case "⟨", "〈":
		return "\\langle "
	case "⟩", "〉":
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
			b.WriteString("\\textasciitilde ")
		case '^':
			b.WriteString("\\textasciicircum ")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
