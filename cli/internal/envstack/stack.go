package envstack

import (
	"os"
	"slices"
	"strings"
)

// Ambient returns the current process environment as a map.
func Ambient() map[string]string {
	environ := os.Environ()
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		if i := strings.IndexByte(kv, '='); i > 0 {
			set(m, kv[:i], kv[i+1:])
		}
	}
	return m
}

// Merge builds the environment for a spawned process from the layered stack.
// Precedence, low → high (later wins): .env / .env.local < ambient shell env
// < .rig.json env < per-command env. Nil layers are ignored.
func Merge(fileEnv, ambient, configEnv, commandEnv map[string]string) map[string]string {
	r := make(map[string]string)
	overlay(r, fileEnv)
	overlay(r, ambient)
	overlay(r, configEnv)
	overlay(r, commandEnv)
	return r
}

func overlay(into, src map[string]string) {
	for k, v := range src {
		set(into, k, v)
	}
}

// Environ flattens a merged map into sorted "KEY=VALUE" form for exec.
func Environ(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out
}
