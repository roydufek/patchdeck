package sshx

import "testing"

// Real-world fixture from Roy's NOVA host (Ubuntu 24.04, arm64): 3 phased updates that
// `apt list --upgradable` shows untagged but `apt-get -s dist-upgrade` reports as
// deferred. They must NOT count as actionable.
func TestParseScanOutput_AllPhasedDeferred(t *testing.T) {
	raw := `apparmor/noble-updates 4.0.1really4.0.1-0ubuntu0.24.04.7 arm64 [upgradable from: 4.0.1really4.0.1-0ubuntu0.24.04.6]
cloud-init/noble-updates 26.1-0ubuntu1~24.04.1 all [upgradable from: 25.3-0ubuntu1~24.04.1]
libapparmor1/noble-updates 4.0.1really4.0.1-0ubuntu0.24.04.7 arm64 [upgradable from: 4.0.1really4.0.1-0ubuntu0.24.04.6]
__DISTUPGRADE_SIM_START__
Reading package lists...
Building dependency tree...
Reading state information...
Calculating upgrade...
The following upgrades have been deferred due to phasing:
  apparmor cloud-init libapparmor1
0 upgraded, 0 newly installed, 0 to remove and 3 not upgraded.
__DISTUPGRADE_SIM_END__
__SYSINFO_START__
PRETTY_NAME="Ubuntu 24.04.1 LTS"
VERSION_ID="24.04"
__SYSINFO_SEP__
up 2 hours, 17 minutes
__SYSINFO_SEP__
6.8.0-51-generic
__SYSINFO_END__`

	res := parseScanOutput(raw, "h1")
	if len(res.Packages) != 0 {
		t.Errorf("actionable Packages = %d (%+v), want 0 — phased updates must not count", len(res.Packages), res.Packages)
	}
	if len(res.DeferredPackages) != 3 {
		t.Fatalf("DeferredPackages = %d, want 3", len(res.DeferredPackages))
	}
	for _, p := range res.DeferredPackages {
		if p.DeferReason != "phased" {
			t.Errorf("%s defer_reason = %q, want \"phased\"", p.Name, p.DeferReason)
		}
	}
	if res.OsName != "Ubuntu 24.04.1 LTS" {
		t.Errorf("OsName = %q, want Ubuntu 24.04.1 LTS (sysinfo parse regressed)", res.OsName)
	}
}

// Mixed: one truly-upgradable package (apt will Inst it) + one phased. The actionable
// one counts; the phased one is deferred.
func TestParseScanOutput_MixedActionableAndDeferred(t *testing.T) {
	raw := `vim/noble-updates 2:9.1.0016-1ubuntu7.8 arm64 [upgradable from: 2:9.1.0016-1ubuntu7.7]
apparmor/noble-updates 4.0.1really4.0.1-0ubuntu0.24.04.7 arm64 [upgradable from: 4.0.1really4.0.1-0ubuntu0.24.04.6]
__DISTUPGRADE_SIM_START__
Calculating upgrade...
The following upgrades have been deferred due to phasing:
  apparmor
Inst vim [2:9.1.0016-1ubuntu7.7] (2:9.1.0016-1ubuntu7.8 Ubuntu:24.04/noble-updates [arm64])
Conf vim (2:9.1.0016-1ubuntu7.8 Ubuntu:24.04/noble-updates [arm64])
1 upgraded, 0 newly installed, 0 to remove and 1 not upgraded.
__DISTUPGRADE_SIM_END__`

	res := parseScanOutput(raw, "h1")
	if len(res.Packages) != 1 || res.Packages[0].Name != "vim" {
		t.Errorf("actionable Packages = %+v, want [vim]", res.Packages)
	}
	if len(res.DeferredPackages) != 1 || res.DeferredPackages[0].Name != "apparmor" {
		t.Errorf("DeferredPackages = %+v, want [apparmor]", res.DeferredPackages)
	}
}

// No simulation block (older agent path / sim failure) → fall back to counting all
// upgradable as actionable rather than wrongly deferring everything.
func TestParseScanOutput_NoSimFallback(t *testing.T) {
	raw := `vim/noble-updates 2:9.1.0016-1ubuntu7.8 arm64 [upgradable from: 2:9.1.0016-1ubuntu7.7]
curl/noble-updates 8.5.0-2ubuntu10.6 arm64 [upgradable from: 8.5.0-2ubuntu10.5]`
	res := parseScanOutput(raw, "h1")
	if len(res.Packages) != 2 {
		t.Errorf("actionable Packages = %d, want 2 (no sim → count all)", len(res.Packages))
	}
	if len(res.DeferredPackages) != 0 {
		t.Errorf("DeferredPackages = %d, want 0 (nothing should be deferred without a sim signal)", len(res.DeferredPackages))
	}
}
