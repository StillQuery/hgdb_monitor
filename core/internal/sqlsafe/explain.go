package sqlsafe

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const MaxExplainSQLBytes = 20 * 1024

var sensitiveSQL = regexp.MustCompile(`(?i)(?:\b(?:\d{1,3}\.){3}\d{1,3}\b|(?:postgres(?:ql)?|https?|ftp):\/\/|\b(?:password|passwd|pwd|token|secret)\s*[:=])`)

// NormalizeExplainInput accepts exactly one read-oriented statement. The
// caller still must execute EXPLAIN inside a read-only transaction.
func NormalizeExplainInput(input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" || len(input) > MaxExplainSQLBytes || strings.IndexByte(input, 0) >= 0 {
		return "", fmt.Errorf("invalid SQL length")
	}
	if sensitiveSQL.MatchString(input) {
		return "", fmt.Errorf("SQL contains connection or credential material")
	}
	keyword, err := leadingKeyword(input)
	if err != nil {
		return "", err
	}
	switch keyword {
	case "select", "with":
	default:
		return "", fmt.Errorf("only SELECT or WITH SELECT can be explained")
	}
	semicolon, err := trailingSemicolon(input)
	if err != nil {
		return "", err
	}
	if semicolon >= 0 {
		input = strings.TrimSpace(input[:semicolon])
	}
	tokens, err := unquotedTokens(input)
	if err != nil {
		return "", err
	}
	for _, token := range tokens {
		switch token {
		case "insert", "update", "delete", "merge", "copy", "call", "do", "create", "alter", "drop", "truncate", "vacuum", "analyze", "reindex", "grant", "revoke":
			return "", fmt.Errorf("data modification and utility statements are not allowed")
		}
	}
	if keyword == "select" {
		for _, token := range tokens {
			if token == "into" {
				return "", fmt.Errorf("SELECT INTO is not allowed")
			}
		}
	} else {
		topLevel, tokenErr := topLevelTokens(input)
		if tokenErr != nil {
			return "", tokenErr
		}
		foundSelect := false
		for _, token := range topLevel {
			if token == "select" {
				foundSelect = true
				break
			}
		}
		if !foundSelect {
			return "", fmt.Errorf("WITH must end in a SELECT statement")
		}
	}
	return input, nil
}

func unquotedTokens(input string) ([]string, error) {
	return lexicalTokens(input, false)
}

func topLevelTokens(input string) ([]string, error) {
	return lexicalTokens(input, true)
}

func lexicalTokens(input string, topLevelOnly bool) ([]string, error) {
	result := make([]string, 0, 32)
	depth := 0
	for index := 0; index < len(input); {
		switch {
		case unicode.IsSpace(rune(input[index])):
			index++
		case input[index] == '\'':
			end, err := quotedEnd(input, index+1, '\'')
			if err != nil {
				return nil, err
			}
			index = end
		case input[index] == '"':
			end, err := quotedEnd(input, index+1, '"')
			if err != nil {
				return nil, err
			}
			index = end
		case input[index] == '$':
			end, ok, err := dollarEnd(input, index)
			if err != nil {
				return nil, err
			}
			if ok {
				index = end
			} else {
				index++
			}
		case index+1 < len(input) && input[index] == '-' && input[index+1] == '-':
			index = lineCommentEnd(input, index+2)
		case index+1 < len(input) && input[index] == '/' && input[index+1] == '*':
			end, err := blockCommentEnd(input, index+2)
			if err != nil {
				return nil, err
			}
			index = end
		case unicode.IsLetter(rune(input[index])) || input[index] == '_':
			start := index
			for index < len(input) && (unicode.IsLetter(rune(input[index])) || unicode.IsDigit(rune(input[index])) || input[index] == '_') {
				index++
			}
			if !topLevelOnly || depth == 0 {
				result = append(result, strings.ToLower(input[start:index]))
			}
		default:
			if input[index] == '(' {
				depth++
			} else if input[index] == ')' {
				if depth == 0 {
					return nil, fmt.Errorf("unbalanced closing parenthesis")
				}
				depth--
			}
			index++
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced parenthesis")
	}
	return result, nil
}

func leadingKeyword(input string) (string, error) {
	index := 0
	for index < len(input) {
		if unicode.IsSpace(rune(input[index])) {
			index++
			continue
		}
		if index+1 < len(input) && input[index] == '-' && input[index+1] == '-' {
			index = lineCommentEnd(input, index+2)
			continue
		}
		if index+1 < len(input) && input[index] == '/' && input[index+1] == '*' {
			var err error
			index, err = blockCommentEnd(input, index+2)
			if err != nil {
				return "", err
			}
			continue
		}
		break
	}
	start := index
	for index < len(input) && (unicode.IsLetter(rune(input[index])) || input[index] == '_') {
		index++
	}
	if start == index {
		return "", fmt.Errorf("SQL must start with a statement keyword")
	}
	return strings.ToLower(input[start:index]), nil
}

func trailingSemicolon(input string) (int, error) {
	semicolon := -1
	for index := 0; index < len(input); {
		switch {
		case input[index] == '\'':
			var err error
			index, err = quotedEnd(input, index+1, '\'')
			if err != nil {
				return -1, err
			}
		case input[index] == '"':
			var err error
			index, err = quotedEnd(input, index+1, '"')
			if err != nil {
				return -1, err
			}
		case input[index] == '$':
			end, ok, err := dollarEnd(input, index)
			if err != nil {
				return -1, err
			}
			if ok {
				index = end
			} else {
				index++
			}
		case index+1 < len(input) && input[index] == '-' && input[index+1] == '-':
			index = lineCommentEnd(input, index+2)
		case index+1 < len(input) && input[index] == '/' && input[index+1] == '*':
			var err error
			index, err = blockCommentEnd(input, index+2)
			if err != nil {
				return -1, err
			}
		case input[index] == ';':
			if semicolon >= 0 {
				return -1, fmt.Errorf("multiple SQL statements are not allowed")
			}
			semicolon = index
			index++
		default:
			if semicolon >= 0 && !unicode.IsSpace(rune(input[index])) {
				return -1, fmt.Errorf("multiple SQL statements are not allowed")
			}
			index++
		}
	}
	return semicolon, nil
}

func quotedEnd(input string, index int, quote byte) (int, error) {
	for index < len(input) {
		if input[index] == quote {
			if index+1 < len(input) && input[index+1] == quote {
				index += 2
				continue
			}
			return index + 1, nil
		}
		if quote == '\'' && input[index] == '\\' && index+1 < len(input) {
			index += 2
		} else {
			index++
		}
	}
	return index, fmt.Errorf("unterminated quoted value")
}

func dollarEnd(input string, index int) (int, bool, error) {
	endTag := index + 1
	for endTag < len(input) && (unicode.IsLetter(rune(input[endTag])) || unicode.IsDigit(rune(input[endTag])) || input[endTag] == '_') {
		endTag++
	}
	if endTag >= len(input) || input[endTag] != '$' {
		return index, false, nil
	}
	tag := input[index : endTag+1]
	closing := strings.Index(input[endTag+1:], tag)
	if closing < 0 {
		return index, true, fmt.Errorf("unterminated dollar-quoted value")
	}
	return endTag + 1 + closing + len(tag), true, nil
}

func lineCommentEnd(input string, index int) int {
	for index < len(input) && input[index] != '\n' {
		index++
	}
	return index
}

func blockCommentEnd(input string, index int) (int, error) {
	depth := 1
	for index < len(input) {
		if index+1 < len(input) && input[index] == '/' && input[index+1] == '*' {
			depth++
			index += 2
		} else if index+1 < len(input) && input[index] == '*' && input[index+1] == '/' {
			depth--
			index += 2
			if depth == 0 {
				return index, nil
			}
		} else {
			index++
		}
	}
	return index, fmt.Errorf("unterminated block comment")
}
