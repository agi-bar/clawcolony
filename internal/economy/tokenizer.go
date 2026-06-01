package economy

import "unicode"

// CalculateToken counts externally visible text cost using the v2 Tian Dao
// tokenizer rules. CJK/Hiragana/Katakana/Hangul and emoji cost 2, everything
// else visible costs 1. Composite emoji are counted per code point component.
func CalculateToken(text string) int64 {
	var total int64
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			total += 2
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r), unicode.Is(unicode.Hangul, r):
			total += 2
		case isEmojiRune(r):
			total += 2
		default:
			total++
		}
	}
	return total
}

func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x26FF:
		return true
	case r >= 0x2700 && r <= 0x27BF:
		return true
	default:
		return false
	}
}
