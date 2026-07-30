// needrestart classification, mirrored from needrestart's default `$nrconf{override_rc}`
// (verified verbatim against needrestart 3.11 on Debian 13). needrestart's DETECTION
// (`needrestart -b`, which drives our `needs_restart`) lists every service on an outdated
// binary — INCLUDING these units. But its RESTART pass (`needrestart -r a`, what our
// "Restart services safely" action runs) deliberately SKIPS them: they can't be safely or
// meaningfully restarted live (dbus/logind sever the session bus, network/docker/gettys are
// disruptive, oneshots like unattended-upgrades just re-run). needrestart defers them to a
// reboot. So the UI must split flagged units into "restartable" (what a restart actually
// clears) vs "deferred" (reboot to apply) — otherwise deferred units linger in the flagged
// count forever and every restart looks like it did nothing.
//
// Anchored at ^ like needrestart's regexes, matched against the unit name as needrestart
// reports it (e.g. "dbus.service", "systemd-logind.service", "unattended-upgrades.service").
const OVERRIDE_RC = [
  /^dbus/,
  // display managers
  /^gdm/, /^greetd/, /^kdm/, /^nodm/, /^sddm/, /^wdm/, /^xdm/, /^lightdm/, /^slim/, /^lxdm/, /^xrdp/,
  // networking
  /^bird/, /^network/, /^NetworkManager/, /^ModemManager/, /^wpa_supplicant/, /^ifup/,
  /^openvpn/, /^quagga/, /^frr/, /^tinc/, /^(open|free|libre|strong)swan/, /^bluetooth/,
  // gettys / per-user manager
  /^getty@.+\.service/, /^serial-getty@.+\.service/, /^user@\d+\.service/,
  // misc daemons needrestart won't auto-restart
  /^usbguard\.service$/, /^zfs-fuse/, /^mythtv-backend/, /^xendomains/, /^lxc/, /^lxcfs/,
  /^libvirt/, /^virtlogd/, /^virtlockd/, /^docker/, /^bacula-/, /^qrtr-ns/, /^rmtfs/,
  // systemd special targets / logind / nspawn
  /^emergency\.service$/, /^rescue\.service$/, /^elogind/, /^systemd-logind/, /^systemd-nspawn/,
  // oneshots (re-running them doesn't clear the flag)
  /^apt-daily\.service$/, /^apt-daily-upgrade\.service$/, /^unattended-upgrades\.service$/,
  /^cron-.*\.service$/, /^rc-local\.service$/,
]

// A flagged unit that needrestart will NOT live-restart → surfaced as "reboot to apply"
// (dbus additionally gets a coordinated-restart option; see isCoordinatedRestartUnit).
export function isNeedrestartDeferred(svc) {
  const s = String(svc || '').trim()
  return OVERRIDE_RC.some(re => re.test(s))
}

// The only deferred units patchdeck can restart WITHOUT a reboot — via needrestart's
// coordinated /etc/needrestart/restart.d/dbus.service handler (restarts the bus AND
// re-registers its clients as a detached transient unit). Everything else deferred is
// reboot-only. (systemd-logind is deferred but NOT coordinated — needrestart itself refuses
// to restart it, Debian bug #798097 — so it's reboot-only.)
const COORDINATED = new Set([
  'dbus', 'dbus.service', 'dbus.socket', 'dbus-broker', 'dbus-broker.service',
])
export function isCoordinatedRestartUnit(svc) {
  return COORDINATED.has(String(svc || '').trim().toLowerCase())
}
