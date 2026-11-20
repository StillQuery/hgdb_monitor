package privacy

import (
	"regexp"
	"strings"
	"unicode"
)

const maxSQLRunes = 2000

var whitespace = regexp.MustCompile(`\s+`)

// RedactSQL preserves identifiers and operators while removing literal values,
// comments and excess length. It is for model-facing diagnostics, not parsing.
func RedactSQL(input string) string {
	return RedactSQLLimit(input, maxSQLRunes)
}

func RedactSQLLimit(input string, maximum int) string {
	var output strings.Builder
	for index := 0; index < len(input); {
		switch {
		case input[index] == '\'':
			output.WriteByte('?')
			index = skipSingleQuoted(input, index)
		case input[index] == '-' && index+1 < len(input) && input[index+1] == '-':
			output.WriteByte(' ')
			index = skipLineComment(input, index)
		case input[index] == '/' && index+1 < len(input) && input[index+1] == '*':
			output.WriteByte(' ')
			index = skipBlockComment(input, index)
		case input[index] == '$':
			if end, ok := skipDollarQuoted(input, index); ok {
				output.WriteByte('?')
				index = end
			} else {
				output.WriteByte(input[index])
				index++
			}
		case isNumberStart(input, index):
			output.WriteByte('?')
			index = skipNumber(input, index)
		default:
			output.WriteByte(input[index])
			index++
		}
	}
	result := strings.TrimSpace(whitespace.ReplaceAllString(output.String(), " "))
	runes := []rune(result)
	if maximum <= 0 {
		maximum = maxSQLRunes
	}
	if len(runes) > maximum {
		return string(runes[:maximum]) + "…"
	}
	return result
}

func skipSingleQuoted(input string, index int) int {
	index++
	for index < len(input) {
		if input[index] == '\'' {
			if index+1 < len(input) && input[index+1] == '\'' {
				index += 2
				continue
			}
			return index + 1
		}
		if input[index] == '\\' && index+1 < len(input) {
			index += 2
		} else {
			index++
		}
	}
	return index
}

func skipLineComment(input string, index int) int {
	for index < len(input) && input[index] != '\n' {
		index++
	}
	return index
}

func skipBlockComment(input string, index int) int {
	depth := 1
	index += 2
	for index < len(input) && depth > 0 {
		if index+1 < len(input) && input[index] == '/' && input[index+1] == '*' {
			depth++
			index += 2
		} else if index+1 < len(input) && input[index] == '*' && input[index+1] == '/' {
			depth--
			index += 2
		} else {
			index++
		}
	}
	return index
}

func skipDollarQuoted(input string, index int) (int, bool) {
	endTag := index + 1
	for endTag < len(input) && (input[endTag] == '_' || unicode.IsLetter(rune(input[endTag])) || unicode.IsDigit(rune(input[endTag]))) {
		endTag++
	}
	if endTag >= len(input) || input[endTag] != '$' {
		return index, false
	}
	tag := input[index : endTag+1]
	closing := strings.Index(input[endTag+1:], tag)
	if closing < 0 {
		return len(input), true
	}
	return endTag + 1 + closing + len(tag), true
}

func isNumberStart(input string, index int) bool {
	if input[index] < '0' || input[index] > '9' {
		return false
	}
	if index == 0 {
		return true
	}
	previous := rune(input[index-1])
	return !unicode.IsLetter(previous) && !unicode.IsDigit(previous) && previous != '_' && previous != '$'
}

func skipNumber(input string, index int) int {
	for index < len(input) {
		character := input[index]
		if (character >= '0' && character <= '9') || character == '.' || character == 'e' || character == 'E' || character == '+' || character == '-' {
			index++
			continue
		}
		break
	}
	return index
}
