set -euo pipefail

check_uid() {
    [[ "$UID" -eq 0 ]] && return
    echo >&2 VPN client not executed as UID 0
    return 1
}

check_caps() {
    local caps expected
    caps=$(grep ^Cap /proc/1/status)
    expected=$(cat <<-'EOF'
		CapInh:	0000000000001000
		CapPrm:	0000000000001000
		CapEff:	0000000000001000
		CapBnd:	0000000000001000
		CapAmb:	0000000000000000
	EOF
    )
    [[ "$caps" == "$expected" ]] && return
    echo >&2 Wrong capability sets:
    echo >&2 "$caps"
    return 1
}

check_dev() {
    local d=/dev/net/tun
    [[ -e "$d" ]] && return
    echo >&2 "$d" not found
    return 1
}

check_profile() {
    [[ -e file ]] && return
    { echo cluster profile file not found; ls --all --recursive; } >&2
    return 1
}

exit_with_test() {
    until [[ -f /logs/marker-file.txt ]]; do sleep 1; done
}

check_uid
check_caps
check_dev
check_profile
exit_with_test &
> /tmp/vpn/up
wait
