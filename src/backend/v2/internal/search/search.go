package search

import (
	"fmt"
	"strconv"
	"strings"
)

const MaxTerms = 24

type ScoreOp uint8

const (
	ScoreEQ ScoreOp = iota
	ScoreGT
	ScoreGTE
	ScoreLT
	ScoreLTE
)

type ScorePredicate struct {
	Op    ScoreOp
	Value int32
}

type Query struct {
	IncludeTags []string
	ExcludeTags []string
	Score       *ScorePredicate
}

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenWord
	tokenMinus
	tokenColon
	tokenCompare
	tokenNumber
)

type token struct {
	kind tokenKind
	text string
	pos  int
}

type lexer struct {
	input string
	pos   int
}

func Parse(input string) (Query, error) {
	l := lexer{input: input}
	var tokens []token
	for {
		tok, err := l.next()
		if err != nil {
			return Query{}, err
		}
		tokens = append(tokens, tok)
		if tok.kind == tokenEOF {
			break
		}
		if len(tokens) > MaxTerms*4+1 {
			return Query{}, fmt.Errorf("search query is too complex")
		}
	}
	return parseTokens(tokens)
}

func (l *lexer) next() (token, error) {
	for l.pos < len(l.input) && isSpace(l.input[l.pos]) {
		l.pos++
	}
	if l.pos >= len(l.input) {
		return token{kind: tokenEOF, pos: l.pos}, nil
	}

	start := l.pos
	switch c := l.input[l.pos]; c {
	case '-':
		l.pos++
		return token{kind: tokenMinus, text: "-", pos: start}, nil
	case ':':
		l.pos++
		return token{kind: tokenColon, text: ":", pos: start}, nil
	case '>', '<', '=':
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '=' && c != '=' {
			l.pos++
		}
		return token{kind: tokenCompare, text: l.input[start:l.pos], pos: start}, nil
	case '"':
		l.pos++
		startText := l.pos
		for l.pos < len(l.input) && l.input[l.pos] != '"' {
			if l.input[l.pos] == '\\' {
				return token{}, fmt.Errorf("search position %d: escapes are not supported", l.pos)
			}
			l.pos++
		}
		if l.pos >= len(l.input) {
			return token{}, fmt.Errorf("search position %d: unterminated quoted tag", start)
		}
		text := strings.TrimSpace(l.input[startText:l.pos])
		l.pos++
		if text == "" {
			return token{}, fmt.Errorf("search position %d: empty quoted tag", start)
		}
		return token{kind: tokenWord, text: text, pos: start}, nil
	}

	if isDigit(l.input[l.pos]) {
		for l.pos < len(l.input) && isDigit(l.input[l.pos]) {
			l.pos++
		}
		return token{kind: tokenNumber, text: l.input[start:l.pos], pos: start}, nil
	}

	for l.pos < len(l.input) {
		c := l.input[l.pos]
		if c == '\\' {
			return token{}, fmt.Errorf("search position %d: backslash is not allowed in an unquoted tag", l.pos)
		}
		if isSpace(c) || c == '-' || c == ':' || c == '>' || c == '<' || c == '=' || c == '"' {
			break
		}
		l.pos++
	}
	if l.pos == start {
		return token{}, fmt.Errorf("search position %d: invalid character %q", start, l.input[start])
	}
	return token{kind: tokenWord, text: l.input[start:l.pos], pos: start}, nil
}

func parseTokens(tokens []token) (Query, error) {
	var out Query
	for i := 0; i < len(tokens) && tokens[i].kind != tokenEOF; {
		excluded := false
		if tokens[i].kind == tokenMinus {
			excluded = true
			i++
			if i >= len(tokens) || tokens[i].kind != tokenWord {
				return Query{}, fmt.Errorf("search position %d: '-' must prefix a tag", tokens[i-1].pos)
			}
		}

		if tokens[i].kind != tokenWord {
			return Query{}, fmt.Errorf("search position %d: expected tag or predicate", tokens[i].pos)
		}
		word := tokens[i]
		i++

		if strings.EqualFold(word.text, "score") && i < len(tokens) && tokens[i].kind == tokenColon {
			if excluded {
				return Query{}, fmt.Errorf("search position %d: score predicate cannot be excluded", word.pos)
			}
			if out.Score != nil {
				return Query{}, fmt.Errorf("search position %d: duplicate score predicate", word.pos)
			}
			i++
			if i >= len(tokens) || tokens[i].kind != tokenCompare {
				return Query{}, fmt.Errorf("search position %d: expected score comparison operator", word.pos)
			}
			op, err := parseScoreOp(tokens[i].text)
			if err != nil {
				return Query{}, fmt.Errorf("search position %d: %w", tokens[i].pos, err)
			}
			i++
			negative := false
			if i < len(tokens) && tokens[i].kind == tokenMinus {
				negative = true
				i++
			}
			if i >= len(tokens) || tokens[i].kind != tokenNumber {
				return Query{}, fmt.Errorf("search position %d: expected integer score", word.pos)
			}
			numeric := tokens[i].text
			if negative {
				numeric = "-" + numeric
			}
			value64, err := strconv.ParseInt(numeric, 10, 32)
			if err != nil {
				return Query{}, fmt.Errorf("search position %d: invalid score", tokens[i].pos)
			}
			out.Score = &ScorePredicate{Op: op, Value: int32(value64)}
			i++
		} else {
			name := normalizeTag(word.text)
			if name == "" {
				return Query{}, fmt.Errorf("search position %d: empty tag", word.pos)
			}
			if len([]rune(name)) > 80 {
				return Query{}, fmt.Errorf("search position %d: tag exceeds 80 characters", word.pos)
			}
			if excluded {
				out.ExcludeTags = appendUnique(out.ExcludeTags, name)
			} else {
				out.IncludeTags = appendUnique(out.IncludeTags, name)
			}
		}

		if len(out.IncludeTags)+len(out.ExcludeTags) > MaxTerms {
			return Query{}, fmt.Errorf("search query has more than %d tags", MaxTerms)
		}
	}
	return out, nil
}

func parseScoreOp(text string) (ScoreOp, error) {
	switch text {
	case "=":
		return ScoreEQ, nil
	case ">":
		return ScoreGT, nil
	case ">=":
		return ScoreGTE, nil
	case "<":
		return ScoreLT, nil
	case "<=":
		return ScoreLTE, nil
	default:
		return 0, fmt.Errorf("unsupported comparison %q", text)
	}
}

func normalizeTag(tag string) string {
	return strings.ToLower(strings.TrimSpace(tag))
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
