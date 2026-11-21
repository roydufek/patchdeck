package sshx

import (
	"regexp"
	"strings"

	"patchdeck/api/internal/models"
)

// packageManagerDetectShell detects the package manager using the remote OS
// identity and the package-manager binaries actually installed on that host.
//
// Debian-family distributions prefer APT. RPM-family distributions prefer
// DNF, then YUM. Unknown distributions use available package-manager binaries
// as a fallback.
const packageManagerDetectShell = `
OS_ID=""
OS_LIKE=""
if [ -r /etc/os-release ]; then
	. /etc/os-release
	OS_ID="${ID:-}"
	OS_LIKE="${ID_LIKE:-}"
fi

PM=""
case " $OS_ID $OS_LIKE " in
	*" debian "*|*" ubuntu "*)
		command -v apt-get >/dev/null 2>&1 && PM="apt"
		;;
	*" amzn "*|*" rhel "*|*" fedora "*|*" centos "*|*" rocky "*|*" almalinux "*|*" ol "*)
		if command -v dnf >/dev/null 2>&1; then
			PM="dnf"
		elif command -v yum >/dev/null 2>&1; then
			PM="yum"
		fi
		;;
esac

# Fallback for compatible distributions whose ID/ID_LIKE is unfamiliar.
if [ -z "$PM" ]; then
	if command -v apt-get >/dev/null 2>&1; then
		PM="apt"
	elif command -v dnf >/dev/null 2>&1; then
		PM="dnf"
	elif command -v yum >/dev/null 2>&1; then
		PM="yum"
	fi
fi
`

var rpmPackageLineRe = regexp.MustCompile(`^(.+)\.([^.]+)$`)

// parseRPMLine parses a DNF/YUM check-update row.
//
// Typical format:
//
//	nginx.x86_64  1:1.24.0-1.el9  appstream
//
// The current version is not reliably included in check-update output,
// so it is intentionally left empty.
func parseRPMLine(line string) (models.PackageInfo, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return models.PackageInfo{}, false
	}

	// Ignore headings, metadata messages and informational output.
	if strings.HasSuffix(fields[0], ":") ||
		strings.EqualFold(fields[0], "Obsoleting") ||
		strings.EqualFold(fields[0], "Last") {
		return models.PackageInfo{}, false
	}

	match := rpmPackageLineRe.FindStringSubmatch(fields[0])
	if match == nil {
		return models.PackageInfo{}, false
	}

	return models.PackageInfo{
		Name:       match[1],
		Arch:       match[2],
		NewVersion: fields[1],
		Source:     fields[2],
	}, true
}
