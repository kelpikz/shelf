// Package utils holds small helpers that more than one package uses.
package utils

// IsASCII is true when every byte of s is ASCII.
func IsASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
