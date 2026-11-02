package hooks

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
)

func DisplayError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(Display(err.Error()))
}

// Display keeps untrusted hook text on one terminal line without interpreting
// control sequences, clipboard OSCs, or invisible direction/format controls.
func Display(s string) string {
	var out strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || !unicode.IsPrint(r) {
			quoted := strconv.QuoteRuneToASCII(r)
			out.WriteString(quoted[1 : len(quoted)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
