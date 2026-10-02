package scout

import (
	"fmt"
	"strings"

	"github.com/joomcode/errorx"
	"github.com/krabiswabbie/busyscout/internal/telnet"
)

// checkIsRemoteDirectory accepts directories, regular files and absent leaves
// with a traversable parent. It does not create or check writability of a file.
func (s *Scout) checkIsRemoteDirectory(remotePath string) (bool, error) {
	if err := telnet.ValidateShellPath(remotePath); err != nil {
		return false, err
	}
	tc, err := s.newClient()
	if err != nil {
		return false, err
	}
	defer tc.Close()

	// Preserve traversal components: cleaning absent/../leaf would hide the
	// missing parent. Include the slash so a nonexistent directory operand
	// ending in / must itself be traversable.
	parent := "."
	if slash := strings.LastIndex(remotePath, "/"); slash >= 0 {
		parent = remotePath[:slash+1]
	}
	parentProbe := parent + "/."
	if strings.HasPrefix(parentProbe, "-") {
		parentProbe = "./" + parentProbe
	}
	linkPath := strings.TrimRight(remotePath, "/")
	if linkPath == "" {
		linkPath = "/"
	}

	// Check links before tests that follow them. If no type test succeeds,
	// diagnostic-only ls -d parent/. distinguishes an absent leaf from a missing
	// or inaccessible parent and keeps stderr; its listing is discarded. The
	// trailing /. requires physical directory traversal without changing cwd
	// or depending on old msh cd -P support.
	command := "bs_path=" + telnet.ShellQuote(remotePath) + "; " +
		"if test -L " + telnet.ShellQuote(linkPath) + "; then printf '%s\\n' 'remote destination is a symlink' >&2; exit 1; " +
		"elif test -d \"$bs_path\"; then printf '%s\\n' directory; " +
		"elif test -f \"$bs_path\"; then printf '%s\\n' file; " +
		"elif test -e \"$bs_path\"; then printf '%s\\n' 'remote destination is not a regular file' >&2; exit 1; " +
		"else ls -d " + telnet.ShellQuote(parentProbe) + " >/dev/null || exit; printf '%s\\n' missing; fi"
	stdout, err := tc.ExecuteChecked(command)
	if err != nil {
		return false, errorx.Decorate(err, "failed to classify remote destination")
	}
	switch strings.TrimSpace(string(stdout)) {
	case "directory":
		return true, nil
	case "file", "missing":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected remote destination classification: %q", stdout)
	}
}
