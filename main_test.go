package main

import (
	"bytes"
	"net"
	"net/netip"
	"testing"
	"time"
)

func mustParseMAC(t *testing.T, s string) macAddr {
	t.Helper()
	mac, err := parseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return mac
}

func TestSendMagicPacket(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	mac := mustParseMAC(t, "00:11:22:33:44:55")
	dst := listener.LocalAddr().(*net.UDPAddr).AddrPort()
	if err := sendMagicPacket(mac, dst); err != nil {
		t.Fatal(err)
	}

	packet := make([]byte, 1024)
	n, _, err := listener.ReadFrom(packet)
	if err != nil {
		t.Fatal(err)
	}
	want := append(bytes.Repeat([]byte{0xff}, 6), bytes.Repeat(mac[:], 16)...)
	if !bytes.Equal(packet[:n], want) {
		t.Fatalf("unexpected magic packet: got %x, want %x", packet[:n], want)
	}
}

func TestParseBroadcast(t *testing.T) {
	for spec, want := range map[string]netip.AddrPort{
		"192.168.0.255":       netip.MustParseAddrPort("192.168.0.255:9"),
		"192.168.0.255:7":     netip.MustParseAddrPort("192.168.0.255:7"),
		"192.168.0.255:65535": netip.MustParseAddrPort("192.168.0.255:65535"),
	} {
		t.Run(spec, func(t *testing.T) {
			got, err := parseBroadcast(spec)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestParseBroadcastErrors(t *testing.T) {
	for _, spec := range []string{
		"",
		"not-an-ip",
		"192.168.0.2555",
		"192.168.0.255:",
		"192.168.0.255:abc",
		"192.168.0.255:70000",
		"::1",
		"[::1]:9",
		"[[192.168.0.255]]",
	} {
		t.Run(spec, func(t *testing.T) {
			if got, err := parseBroadcast(spec); err == nil {
				t.Fatalf("expected an error, got %v", got)
			}
		})
	}
}

func TestParseMAC(t *testing.T) {
	if got, want := mustParseMAC(t, "00-11-22-33-44-55"), (macAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}); got != want {
		t.Fatalf("parseMAC: got %v, want %v", got, want)
	}
	for _, s := range []string{"", "invalid", "00:11:22:33:44:55:66:77"} {
		t.Run(s, func(t *testing.T) {
			if _, err := parseMAC(s); err == nil {
				t.Fatal("expected an error for an invalid or non-6-byte MAC address")
			}
		})
	}
}

func TestParseWOLTargets(t *testing.T) {
	targets, err := parseWOLTargets(" desktop = 00:11:22:33:44:55@192.168.1.255 , nas=AA:BB:CC:DD:EE:FF@192.168.1.255:7, ")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}
	desktop := targets["desktop"]
	if want := (macAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}); desktop.mac != want {
		t.Errorf("desktop.mac: got %v, want %v", desktop.mac, want)
	}
	if want := netip.MustParseAddrPort("192.168.1.255:9"); desktop.broadcast != want {
		t.Errorf("desktop.broadcast: got %v, want %v", desktop.broadcast, want)
	}
	if want := netip.MustParseAddrPort("192.168.1.255:7"); targets["nas"].broadcast != want {
		t.Errorf("nas.broadcast: got %v, want %v", targets["nas"].broadcast, want)
	}
}

func TestParseWOLTargetsDefaultBroadcast(t *testing.T) {
	targets, err := parseWOLTargets("desktop=00:11:22:33:44:55")
	if err != nil {
		t.Fatal(err)
	}
	if got := targets["desktop"].broadcast; got != defaultBroadcast {
		t.Fatalf("got %v, want %v", got, defaultBroadcast)
	}
}

func TestParseWOLTargetsErrors(t *testing.T) {
	for name, spec := range map[string]string{
		"missing equals":    "desktop",
		"missing name":      "=00:11:22:33:44:55",
		"invalid MAC":       "desktop=invalid",
		"8-byte MAC":        "desktop=00:11:22:33:44:55:66:77",
		"missing MAC":       "desktop=@192.168.1.255",
		"duplicate name":    "desktop=00:11:22:33:44:55,desktop=AA:BB:CC:DD:EE:FF",
		"invalid broadcast": "desktop=00:11:22:33:44:55@not-an-ip",
		"empty broadcast":   "desktop=00:11:22:33:44:55@",
		"bad port":          "desktop=00:11:22:33:44:55@192.168.0.255:abc",
		"IPv6 broadcast":    "desktop=00:11:22:33:44:55@[::1]:9",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWOLTargets(spec); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestTargetChoices(t *testing.T) {
	targets, err := parseWOLTargets("nas=00:11:22:33:44:55,desktop=00:11:22:33:44:56,desk-lamp=00:11:22:33:44:57")
	if err != nil {
		t.Fatal(err)
	}

	got := targetChoices(targets, "DESK")
	if len(got) != 2 || got[0].Value != "desk-lamp" || got[1].Value != "desktop" {
		t.Fatalf("unexpected choices: %v", got)
	}
	if got := targetChoices(targets, ""); len(got) != 3 {
		t.Fatalf("got %d choices for an empty prefix, want 3", len(got))
	}
	if got := targetChoices(targets, "nope"); len(got) != 0 {
		t.Fatalf("got %d choices for an unmatched prefix, want 0", len(got))
	}
}

func TestTargetChoicesLimit(t *testing.T) {
	targets := make(map[string]wolTarget)
	for i := range 30 {
		targets[string(rune('a'+i))] = wolTarget{}
	}
	if got := len(targetChoices(targets, "")); got != maxTargetChoices {
		t.Fatalf("got %d choices, want %d", got, maxTargetChoices)
	}
}
