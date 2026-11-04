package parser

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/token"
)

// A file's full import path, `db::posts`, is written with `::` between its
// segments and no space around it. joinPaths reads each such run of tokens
// as one PATH token, so every position that takes a file's short name as a
// qualifier (`posts.post_by_id`, `posts.Post`) takes its path the same way.
// A `::` that does not join two adjacent segments stays a COLONCOLON, which
// no grammar rule accepts.

// joinPaths returns tokens with every `seg::seg...` run joined into one
// PATH token. The first segment is a name or `..` (a relative import); a
// segment after `::` may be any word, a keyword included, since a file may
// be named `defer.nomi`. tokens itself is not changed.
func joinPaths(tokens []token.Token) []token.Token {
	var out []token.Token
	for i := 0; i < len(tokens); i++ {
		end := pathRunEnd(tokens, i)
		if end == i {
			if out != nil {
				out = append(out, tokens[i])
			}
			continue
		}
		if out == nil {
			out = append(make([]token.Token, 0, len(tokens)), tokens[:i]...)
		}
		parts := []string{tokens[i].Lexeme}
		for j := i + 2; j <= end; j += 2 {
			parts = append(parts, tokens[j].Lexeme)
		}
		last := tokens[end]
		out = append(out, token.Token{
			Type:    token.PATH,
			Lexeme:  strings.Join(parts, ast.PathSeparator),
			Line:    tokens[i].Line,
			Col:     tokens[i].Col,
			EndLine: last.Line,
			EndCol:  last.Col + len(last.Lexeme),
		})
		i = end
	}
	if out == nil {
		return tokens
	}
	return out
}

// pathRunEnd is the index of the last segment of the path that starts at i,
// or i when none does.
func pathRunEnd(tokens []token.Token, i int) int {
	first := tokens[i]
	if first.Type != token.DOTDOT && !isPathWord(first) {
		return i
	}
	end := i
	for end+2 < len(tokens) {
		sep, seg := tokens[end+1], tokens[end+2]
		if sep.Type != token.COLONCOLON || !adjacent(tokens[end], sep) || !adjacent(sep, seg) || !isPathWord(seg) {
			break
		}
		end += 2
	}
	return end
}

// isPathWord reports whether t is spelled as a file or directory name: a
// word, which may be a keyword.
func isPathWord(t token.Token) bool {
	return t.Type != token.DOTDOT && t.Type != token.STRING_LITERAL && isImportPathSegment(t)
}

// pathSegments splits a PATH token into its segments, each at its own
// position. It is nil for any other token.
func pathSegments(t token.Token) []ast.PathSegment {
	if t.Type != token.PATH {
		return nil
	}
	parts := strings.Split(t.Lexeme, ast.PathSeparator)
	segs := make([]ast.PathSegment, len(parts))
	col := t.Col
	for i, part := range parts {
		segs[i] = ast.PathSegment{Name: part, Line: t.Line, Col: col}
		col += len(part) + len(ast.PathSeparator)
	}
	return segs
}

// isQualifierHead reports whether t can start a qualified name: a file's
// short name or its full path.
func isQualifierHead(t token.Token) bool {
	return t.Type == token.IDENT || t.Type == token.PATH
}

// strayPathError is the error for a `::` that joins no path.
func strayPathError(t token.Token) error {
	return errorAt(t.Line, t.Col, "`::` joins the lowercase segments of a file's path, with no space around it, as in `std::io` or `db::posts.post_by_id`")
}

// filePathAlone is the error for a full path that is not followed by a
// member: a file is not a value.
func filePathAlone(t token.Token) error {
	return errorAt(t.Line, t.Col, "`%s` is a file, not a value; name one of its members, as in `%s.name`", t.Lexeme, t.Lexeme)
}

// pathSegmentType is the token type a path segment was read as: a
// capitalized name is a TYPE_IDENT, as it is anywhere else.
func pathSegmentType(name string) token.TokenType {
	if name != "" && name[0] >= 'A' && name[0] <= 'Z' {
		return token.TYPE_IDENT
	}
	return token.IDENT
}
