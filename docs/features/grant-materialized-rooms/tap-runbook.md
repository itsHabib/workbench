# custody tap listener — reach runbook

**Purpose**: Firewall rules that restrict inbound access to the custody tap listener
(`-tap-addr`) to the room subnet only, plus preflight checks to verify the rules
are in force before starting the listener. Run this on the rooms-host before
enabling the tap listener.

---

## Prerequisites

- `custody` binary installed on the rooms-host.
- The room tap interface is up (e.g., `tap0`). Confirm:
  ```
  ip link show tap0
  ip addr show tap0
  ```
  Record the interface name. If it differs from `tap0`, pass `-tap-if-prefix <prefix>` when starting custody.
- The room subnet CIDR (e.g., `10.0.100.0/24`). Supplied by the rooms operator.
- The custody port (default `8127`).

---

## 1. Verify the tap interface address

```bash
TAP_IFACE=tap0              # adjust if different
CUSTODY_PORT=8127
TAP_IP=$(ip -4 addr show "$TAP_IFACE" | awk '/inet /{print $2}' | cut -d/ -f1)
echo "Tap IP: $TAP_IP"
```

This is the address to pass as `-tap-addr $TAP_IP:$CUSTODY_PORT`.

---

## 2. Apply the firewall rules

Choose **nftables** (preferred) or **iptables** depending on what the rooms-host uses.

> **Do NOT install a host-wide default-drop policy.** These rules scope a `drop`
> to the custody port only, leaving every other port (SSH, etc.) untouched. A
> base `input` chain with `policy drop` would silently drop *new* inbound
> connections that don't match an accept rule — including a fresh SSH session —
> and can lock you out of the rooms-host. The chain policy below is `accept`;
> only traffic to the custody port from outside the room subnet is dropped.

### 2a. nftables

```bash
ROOM_SUBNET=10.0.100.0/24  # replace with actual room subnet
CUSTODY_PORT=8127

nft add table inet custody_tap 2>/dev/null || true
# policy ACCEPT — this chain only restricts the custody port; it must not
# govern the rest of the host's inbound traffic.
nft add chain inet custody_tap input \
    "{ type filter hook input priority 0; policy accept; }"
# Allow ONLY the room subnet to reach the custody port.
nft add rule inet custody_tap input \
    ip saddr "$ROOM_SUBNET" tcp dport "$CUSTODY_PORT" accept
# Log and drop any OTHER source reaching the custody port. Everything not
# destined for the custody port falls through to `policy accept`, untouched.
nft add rule inet custody_tap input \
    tcp dport "$CUSTODY_PORT" log prefix "custody-tap-drop: " drop
```

**IPv6 room subnets:** if guests reach the room over IPv6 (pure IPv6 or a ULA),
add the `ip6` twin of the accept rule — the `ip saddr` rule above matches IPv4
only, so an IPv6 guest would fall to the port drop:

```bash
ROOM_SUBNET6=fd00:100::/64  # the room's IPv6 subnet, if any
nft add rule inet custody_tap input \
    ip6 saddr "$ROOM_SUBNET6" tcp dport "$CUSTODY_PORT" accept
```

Persist across reboots:

```bash
nft list ruleset > /etc/nftables.d/custody-tap.nft
# Ensure /etc/nftables.conf includes: include "/etc/nftables.d/*.nft"
```

### 2b. iptables (fallback)

```bash
ROOM_SUBNET=10.0.100.0/24
CUSTODY_PORT=8127

# Allow the room subnet to the custody port, then drop only OTHER sources to
# that port. No -P INPUT DROP: the default policy stays untouched so SSH and
# everything else keep working.
iptables -A INPUT -s "$ROOM_SUBNET" -p tcp --dport "$CUSTODY_PORT" -j ACCEPT
iptables -A INPUT -p tcp --dport "$CUSTODY_PORT" -j DROP

# Persist (Debian/Ubuntu):
iptables-save > /etc/iptables/rules.v4
```

**IPv6 room subnets:** the rules above are IPv4-only (`iptables`). For an IPv6
room subnet, add the `ip6tables` twin (an IPv4-only ACCEPT won't match an IPv6
guest, so it would be dropped):

```bash
ROOM_SUBNET6=fd00:100::/64
ip6tables -A INPUT -s "$ROOM_SUBNET6" -p tcp --dport "$CUSTODY_PORT" -j ACCEPT
ip6tables -A INPUT -p tcp --dport "$CUSTODY_PORT" -j DROP
ip6tables-save > /etc/iptables/rules.v6
```

---

## Preflight checks {#preflight}

Run these **before** starting `custody serve -tap-addr`. The custody binary also
runs a programmatic preflight check at startup via `PreflightFirewall` (see
`cmd/custody/internal/serve/tap.go`) but the manual checks below give faster
feedback and confirm the right tool is installed.

### Check: nftables rule present

The preflight requires BOTH rules on the port — a source-restricted accept
(room may reach) AND a drop/reject (every other source denied). Under the
policy-ACCEPT chain the deny rule is what actually restricts the port; without
it, non-room traffic falls through to `policy accept`. `reject` (with tcp
reset) counts as a deny alongside `drop`. These greps mirror what the prober
checks, so a partial config produces no output:

```bash
# Accept half: a rule with saddr + this dport + accept.
nft list ruleset | awk "/saddr/ && /dport $CUSTODY_PORT / && /accept/"
# Deny half: a rule with this dport + drop or reject.
nft list ruleset | awk "/dport $CUSTODY_PORT / && (/drop/ || /reject/)"
# Both must print a line. A bare "dport 8127" grep would over-report.
```

### Check: iptables rule present (if not using nftables)

```bash
# Accept half: -s + --dport + -j ACCEPT on one line.
iptables-save | grep -- "--dport $CUSTODY_PORT " | grep -- "-s " | grep -- "-j ACCEPT"
# Deny half: --dport + (DROP or REJECT).
iptables-save | grep -- "--dport $CUSTODY_PORT " | grep -E -- "-j (DROP|REJECT)"
# For an IPv6-only room subnet, run the same against ip6tables-save — the probe
# reads it too, so the equivalent ip6tables ACCEPT + DROP/REJECT pair satisfies.
```

### Check: tap interface has the expected IP

```bash
ip addr show "$TAP_IFACE" | grep "inet "
```

### Check: custody will accept the tap bind

```bash
custody serve -tap-addr "$TAP_IP:$CUSTODY_PORT" -tap-if-prefix "$TAP_IFACE_PREFIX" \
    -state /path/to/state -mint-key-dir /path/to/keys \
    # This will fail at startup if any preflight check fails.
    # Read the coded error for the remedy.
```

---

## 3. Record the interface name override (if needed)

If the tap interface is not named `tap0` / `tap1` / … (the default prefix is
`tap`), record the override for the operator:

```bash
# Example: interface is "gtap0" → pass -tap-if-prefix gtap
echo "Set -tap-if-prefix to the prefix of $TAP_IFACE" >> deployment-notes.txt
```

---

## Coded errors at startup

| Code | Meaning | Remedy |
|---|---|---|
| `refused_wildcard_bind` | `-tap-addr` is a wildcard (`0.0.0.0`, `::`) | Use the tap interface's concrete IP |
| `refused_non_tap_bind` | Address not on a tap-prefixed interface | Check `-tap-if-prefix` matches the interface name |
| `refused_bad_tap_addr` | Host part is not a valid IP | Fix the address format |
| `refused_preflight_no_rule` | Firewall rule not detected | Apply §2 rules above, then retry |
| `refused_preflight_error` | Firewall probe command failed | Install `nft` or `iptables-save`, then retry |

---

## Per-request coded errors (tap listener only)

| Code | Meaning | Remedy |
|---|---|---|
| `refused_unbound_on_tap` | Grant has no `bound_source` | Derive a bound child: `custody derive -grant <parent> -bound-source <ip> …` |
| `refused_grant_source_invalid` | Grant's `bound_source` is not a valid IP | Grant record is malformed; re-derive a bound child |
| `refused_source_mismatch` | Transport source ≠ grant's `bound_source` | Originate the request from the bound IP, or derive a new grant |

---

## Validation on the rooms-host

After applying the rules, run `custody serve` with `-tap-addr` and confirm:

1. Startup prints `custody serve: tap listener on <ip>:8127` (no preflight error).
2. From a room guest VM: `curl -H "X-Custody-Grant: <bound-token>" http://<tap-ip>:8127/<key>/...` returns the expected response (not a 401 source mismatch).
3. From a host outside the room subnet: the connection is rejected at the firewall (TCP RST or timeout).
