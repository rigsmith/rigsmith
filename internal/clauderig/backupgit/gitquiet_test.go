package backupgit

// git's post-commit maintenance runs detached and races t.TempDir's cleanup;
// see the package comment for what that failure looks like.
import _ "github.com/rigsmith/rigsmith/internal/gitquiet"
