// git-cgi-server is a minimal smart-HTTP git server for the josh-proxy smoke
// test (scripts/smoke/josh-proxy-smoke.sh). It serves every repo under -root
// read/write on a free localhost port by wrapping `git http-backend` as a CGI
// handler, and prints PORT=<n> once listening. With -pick it just prints a
// free port and exits — a portable way to choose josh-proxy's own port.
//
// A real Go CGI wrapper (not `python -m http.server`) because the smoke test
// needs the smart protocol with push support: dumb HTTP can't receive-pack,
// and Python's CGI handler was removed in 3.13. Pushes additionally need
// `http.receivepack=true` on the bare repo; the smoke script sets that.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/cgi"
	"os/exec"
)

func main() {
	pick := flag.Bool("pick", false, "print a free localhost port and exit")
	root := flag.String("root", ".", "directory containing the bare repos to serve")
	flag.Parse()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("PORT=%d\n", ln.Addr().(*net.TCPAddr).Port)
	if *pick {
		ln.Close()
		return
	}

	git, err := exec.LookPath("git")
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.Serve(ln, &cgi.Handler{
		Path: git,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + *root, "GIT_HTTP_EXPORT_ALL=1"},
		// http-backend spawns git subprocesses; SYSTEMROOT is required for
		// networking on Windows.
		InheritEnv: []string{"PATH", "SYSTEMROOT"},
	}))
}
