package sessions

import (
	"bufio"
	"io"
)

// maxLineBytes caps one rollout line. A file with one enormous newline-free line
// would otherwise set the process's memory ceiling, and a search reads every
// rollout it can see.
const maxLineBytes = 4 << 20

func newScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	return sc
}
