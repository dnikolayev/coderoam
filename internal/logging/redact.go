package logging

import "regexp"

var (
	phonePattern = regexp.MustCompile(`\+?\d[\d\s().-]{7,}\d`)
	jidPattern   = regexp.MustCompile(`\b\d{5,}(@s\.whatsapp\.net|@g\.us)\b`)
)

func Redact(value string) string {
	value = phonePattern.ReplaceAllStringFunc(value, redactDigits)
	value = jidPattern.ReplaceAllStringFunc(value, func(s string) string {
		return redactDigits(s)
	})
	return value
}

func redactDigits(value string) string {
	digits := make([]int, 0, len(value))
	for i, r := range value {
		if r >= '0' && r <= '9' {
			digits = append(digits, i)
		}
	}
	if len(digits) <= 6 {
		return value
	}
	out := []rune(value)
	for i, pos := range digits {
		if i < 3 || i >= len(digits)-3 {
			continue
		}
		out[pos] = '*'
	}
	return string(out)
}
