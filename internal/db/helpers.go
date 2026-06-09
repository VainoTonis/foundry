package db

import (
	"strconv"
	"strings"
)

func itoa(n int) string {
	return strconv.Itoa(n)
}

func joinComma(s []string) string {
	return strings.Join(s, ", ")
}
