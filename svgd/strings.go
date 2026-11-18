package svgd

import "strings"

func IsNonEmpty(s string) bool {
	return strings.TrimSpace(s) != ""
}

func SplitKeep(s string) []string {
	if s == "" {
		return nil
	}
	return strings.SplitAfter(s, "\n")
}

func Atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}
