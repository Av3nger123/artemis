package utils

import (
	"regexp"
	"strings"
)

func Slugify(s string) string {
	s = strings.ToLower(s)
	reg, err := regexp.Compile("[^a-z0-9]+")
	if err != nil {
		panic(err)
	}
	s = reg.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
