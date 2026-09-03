// Package sexpr parses the inert S-expression syntax shared by shell3.lisp
// configuration and *.wrk.lisp workflow files.
package sexpr

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/participle/v2/lexer"
)

// Kind identifies the four values the shell3 reader understands. This is a
// data language, not a Lisp evaluator: quotes, cons cells, and reader macros
// deliberately do not exist.
type Kind uint8

const (
	List Kind = iota
	Symbol
	String
	Number
)

// Position identifies the first byte of a node in its source.
type Position struct {
	Filename string
	Offset   int
	Line     int
	Column   int
}

func (p Position) String() string {
	if p.Filename == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Column)
	}
	return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Column)
}

// Node is one parsed value. Value is populated for Symbol and String, Integer
// for Number, and Children for List.
type Node struct {
	Kind     Kind
	Pos      Position
	Value    string
	Integer  int64
	Children []Node
}

func (n Node) Head() (string, bool) {
	if n.Kind != List || len(n.Children) == 0 || n.Children[0].Kind != Symbol {
		return "", false
	}
	return n.Children[0].Value, true
}

// Form returns a list's leading symbol and remaining values.
func (n Node) Form() (string, []Node, bool) {
	head, ok := n.Head()
	if !ok {
		return "", nil, false
	}
	return head, n.Children[1:], true
}

// ValidName reports whether s is safe as a stable configuration, node, and
// filesystem identifier. Provider values and event names are not names.
func ValidName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') || (i > 0 && (r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return true
}

var definition = lexer.MustStateful(lexer.Rules{
	"Root": {
		{Name: "whitespace", Pattern: `[ \t\r\n]+`},
		{Name: "comment", Pattern: `;[^\r\n]*`},
		{Name: "LParen", Pattern: `\(`},
		{Name: "RParen", Pattern: `\)`},
		{Name: "RawString", Pattern: `"""`, Action: lexer.Push("RawString")},
		{Name: "String", Pattern: `"(?:\\.|[^"\\])*"`},
		{Name: "Atom", Pattern: `[^\s()";]+`},
	},
	"RawString": {
		{Name: "RawStringEnd", Pattern: `"""`, Action: lexer.Pop()},
		{Name: "RawStringText", Pattern: `[^\"]+|"{1,2}`},
	},
})

type parser struct {
	filename string
	lex      lexer.Lexer
	types    map[string]lexer.TokenType
	peeked   *lexer.Token
}

// Parse reads all top-level forms in src. Structural syntax is checked here;
// configuration and wrk packages apply their own strict semantic schemas.
func Parse(filename string, src []byte) ([]Node, error) {
	l, err := definition.LexString(filename, string(src))
	if err != nil {
		return nil, err
	}
	p := &parser{filename: filename, lex: l, types: definition.Symbols()}
	var forms []Node
	for {
		tok, err := p.peek()
		if err != nil {
			return nil, err
		}
		if tok.EOF() {
			return forms, nil
		}
		n, err := p.readNode()
		if err != nil {
			return nil, err
		}
		forms = append(forms, n)
	}
}

func (p *parser) readNode() (Node, error) {
	tok, err := p.next()
	if err != nil {
		return Node{}, err
	}
	switch tok.Type {
	case p.types["LParen"]:
		return p.readList(tok)
	case p.types["RParen"]:
		return Node{}, p.errorf(tok, "unexpected )")
	case p.types["String"]:
		value, err := strconv.Unquote(tok.Value)
		if err != nil {
			return Node{}, p.errorf(tok, "invalid string: %v", err)
		}
		return Node{Kind: String, Pos: position(tok), Value: value}, nil
	case p.types["RawString"]:
		return p.readRawString(tok)
	case p.types["Atom"]:
		if i, err := strconv.ParseInt(tok.Value, 10, 64); err == nil {
			return Node{Kind: Number, Pos: position(tok), Integer: i}, nil
		}
		return Node{Kind: Symbol, Pos: position(tok), Value: tok.Value}, nil
	case lexer.EOF:
		return Node{}, p.errorf(tok, "unexpected end of file")
	default:
		return Node{}, p.errorf(tok, "unexpected token %q", tok.Value)
	}
}

func (p *parser) readList(open lexer.Token) (Node, error) {
	n := Node{Kind: List, Pos: position(open)}
	for {
		tok, err := p.peek()
		if err != nil {
			return Node{}, err
		}
		if tok.EOF() {
			return Node{}, p.errorf(open, "list is not closed")
		}
		if tok.Type == p.types["RParen"] {
			_, _ = p.next()
			return n, nil
		}
		child, err := p.readNode()
		if err != nil {
			return Node{}, err
		}
		n.Children = append(n.Children, child)
	}
}

func (p *parser) readRawString(open lexer.Token) (Node, error) {
	var body strings.Builder
	for {
		tok, err := p.next()
		if err != nil {
			return Node{}, err
		}
		switch tok.Type {
		case p.types["RawStringText"]:
			body.WriteString(tok.Value)
		case p.types["RawStringEnd"]:
			return Node{Kind: String, Pos: position(open), Value: body.String()}, nil
		case lexer.EOF:
			return Node{}, p.errorf(open, "raw string is not closed")
		default:
			return Node{}, p.errorf(tok, "unexpected token in raw string")
		}
	}
}

func (p *parser) peek() (lexer.Token, error) {
	if p.peeked != nil {
		return *p.peeked, nil
	}
	tok, err := p.lex.Next()
	if err != nil {
		return lexer.Token{}, err
	}
	p.peeked = &tok
	return tok, nil
}

func (p *parser) next() (lexer.Token, error) {
	if p.peeked != nil {
		tok := *p.peeked
		p.peeked = nil
		return tok, nil
	}
	return p.lex.Next()
}

func (p *parser) errorf(tok lexer.Token, format string, args ...any) error {
	return fmt.Errorf("%s: %s", position(tok), fmt.Sprintf(format, args...))
}

func position(tok lexer.Token) Position {
	return Position{
		Filename: tok.Pos.Filename,
		Offset:   tok.Pos.Offset,
		Line:     tok.Pos.Line,
		Column:   tok.Pos.Column,
	}
}
