package journal

// The tests here can reach code that runs git. The import makes that git
// hermetic — see internal/testgit for what the machine would otherwise put
// in the repos they build.
import _ "github.com/rigsmith/rigsmith/internal/testgit"
