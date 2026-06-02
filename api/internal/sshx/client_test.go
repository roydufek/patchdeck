package sshx

import "testing"

func TestParseScanOutput_Packages(t *testing.T) {
	raw := "Listing...\n" +
		"libfoo/jammy-security 1.2.3-1 amd64 [upgradable from: 1.2.2-1]\n" +
		"libbar/jammy-updates 2.0-1 amd64 [upgradable from: 1.9-1]\n"
	res := parseScanOutput(raw, "host-1")

	if res.HostID != "host-1" {
		t.Fatalf("HostID = %q, want host-1", res.HostID)
	}
	if len(res.Packages) != 2 {
		t.Fatalf("got %d packages, want 2: %+v", len(res.Packages), res.Packages)
	}
	p := res.Packages[0]
	if p.Name != "libfoo" || p.Source != "jammy-security" || p.NewVersion != "1.2.3-1" || p.Arch != "amd64" || p.CurrentVersion != "1.2.2-1" {
		t.Fatalf("first package parsed wrong: %+v", p)
	}
}

func TestParseScanOutput_PhasedExcluded(t *testing.T) {
	raw := "Listing...\n" +
		"libfoo/jammy-security 1.2.3-1 amd64 [upgradable from: 1.2.2-1]\n" +
		"libphased/jammy-updates 2.0-1 amd64 [upgradable from: 1.9-1] (phased 20%)\n"
	res := parseScanOutput(raw, "h")
	if len(res.Packages) != 1 {
		t.Fatalf("phased update should be excluded; got %d packages: %+v", len(res.Packages), res.Packages)
	}
	if res.Packages[0].Name != "libfoo" {
		t.Fatalf("wrong package kept: %+v", res.Packages[0])
	}
}

func TestParseScanOutput_RebootAndPackages(t *testing.T) {
	raw := "Listing...\n" +
		"__REBOOT__\n" +
		"__REBOOT_PKGS_START__\n" +
		"linux-image-generic\n" +
		"libc6\n" +
		"__REBOOT_PKGS_END__\n"
	res := parseScanOutput(raw, "h")
	if !res.NeedsReboot {
		t.Fatal("NeedsReboot should be true when __REBOOT__ present")
	}
	if res.RebootReason != "linux-image-generic, libc6" {
		t.Fatalf("RebootReason = %q", res.RebootReason)
	}
}

func TestParseScanOutput_Needrestart(t *testing.T) {
	withSvc := "NEEDRESTART-SVC: apache2.service\nNEEDRESTART-SVC: cron.service\n"
	res := parseScanOutput(withSvc, "h")
	if !res.NeedrestartFound {
		t.Fatal("NeedrestartFound should be true when marker absent")
	}
	if len(res.NeedsRestart) != 2 || res.NeedsRestart[0] != "apache2.service" || res.NeedsRestart[1] != "cron.service" {
		t.Fatalf("NeedsRestart parsed wrong (expect trimmed names): %+v", res.NeedsRestart)
	}

	missing := "__NEEDRESTART_MISSING__\n"
	res2 := parseScanOutput(missing, "h")
	if res2.NeedrestartFound {
		t.Fatal("NeedrestartFound should be false when __NEEDRESTART_MISSING__ present")
	}
}

func TestParseScanOutput_AptUpdateFailed(t *testing.T) {
	ok := parseScanOutput("libfoo/jammy 1.2 amd64 [upgradable from: 1.1]\n", "h")
	if ok.AptUpdateFailed {
		t.Fatal("AptUpdateFailed should be false when marker absent")
	}
	failed := parseScanOutput("__APT_UPDATE_FAILED__\nlibfoo/jammy 1.2 amd64 [upgradable from: 1.1]\n", "h")
	if !failed.AptUpdateFailed {
		t.Fatal("AptUpdateFailed should be true when __APT_UPDATE_FAILED__ present")
	}
	// A failed apt-get update must NOT stop package parsing (uses cached lists).
	if len(failed.Packages) != 1 {
		t.Fatalf("packages should still parse after update failure; got %d", len(failed.Packages))
	}
}

func TestParseScanOutput_Sysinfo(t *testing.T) {
	raw := "libfoo/jammy 1.2 amd64 [upgradable from: 1.1]\n" +
		"__SYSINFO_START__\n" +
		"PRETTY_NAME=\"Ubuntu 22.04.4 LTS\"\n" +
		"VERSION_ID=\"22.04\"\n" +
		"__SYSINFO_SEP__\n" +
		"up 3 days, 4 hours\n" +
		"__SYSINFO_SEP__\n" +
		"5.15.0-101-generic\n" +
		"__SYSINFO_END__\n"
	res := parseScanOutput(raw, "h")
	if res.OsName != "Ubuntu 22.04.4 LTS" {
		t.Fatalf("OsName = %q", res.OsName)
	}
	if res.OsVersion != "22.04" {
		t.Fatalf("OsVersion = %q", res.OsVersion)
	}
	if res.Uptime != "3 days, 4 hours" {
		t.Fatalf("Uptime = %q", res.Uptime)
	}
	if res.Kernel != "5.15.0-101-generic" {
		t.Fatalf("Kernel = %q", res.Kernel)
	}
	// sysinfo block must be stripped before package parsing — no stray packages.
	if len(res.Packages) != 1 {
		t.Fatalf("got %d packages, want 1 (sysinfo must not leak): %+v", len(res.Packages), res.Packages)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\nb\nc\nd\n", 2); got != "c | d" {
		t.Fatalf("lastLines = %q, want 'c | d'", got)
	}
	if got := lastLines("   ", 3); got != "" {
		t.Fatalf("lastLines(blank) = %q, want empty", got)
	}
}
