package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// macAddr is a 6-byte hardware address. net.ParseMAC also accepts 8-byte
// EUI-64 addresses, and a magic packet built from one would repeat the wrong
// stride, so the fixed-size array makes that state unrepresentable.
type macAddr [6]byte

// wolTarget is one machine the bot can wake.
type wolTarget struct {
	mac       macAddr
	broadcast netip.AddrPort
}

// defaultBroadcast is the limited broadcast address used when a target does not
// name one.
var defaultBroadcast = netip.AddrPortFrom(netip.AddrFrom4([4]byte{255, 255, 255, 255}), 9)

func parseMAC(s string) (macAddr, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return macAddr{}, fmt.Errorf("parse MAC address %q: %w", s, err)
	}
	if len(hw) != 6 {
		return macAddr{}, fmt.Errorf("Wake-on-LAN requires a 6-byte MAC address, got %d bytes in %q", len(hw), s)
	}
	var mac macAddr
	copy(mac[:], hw)
	return mac, nil
}

// parseWOLTargets turns the WOL_TARGETS setting into a registry keyed by
// target name. The format is a comma-separated list of name=MAC[@broadcast]:
//
//	desktop=00:11:22:33:44:55@192.168.1.255,nas=AA:BB:CC:DD:EE:FF@192.168.1.255:7
//
// The broadcast address defaults to 255.255.255.255:9.
func parseWOLTargets(spec string) (map[string]wolTarget, error) {
	targets := make(map[string]wolTarget)
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, rest, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("Wake-on-LAN target %q must be name=MAC[@broadcast]", entry)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("Wake-on-LAN target %q has no name", entry)
		}
		if _, dup := targets[name]; dup {
			return nil, fmt.Errorf("Wake-on-LAN target %q is defined twice", name)
		}
		macSpec, broadcastSpec, hasBroadcast := strings.Cut(strings.TrimSpace(rest), "@")
		mac, err := parseMAC(strings.TrimSpace(macSpec))
		if err != nil {
			return nil, fmt.Errorf("Wake-on-LAN target %q: %w", name, err)
		}
		broadcast := defaultBroadcast
		if hasBroadcast {
			broadcast, err = parseBroadcast(strings.TrimSpace(broadcastSpec))
			if err != nil {
				return nil, fmt.Errorf("Wake-on-LAN target %q: %w", name, err)
			}
		}
		targets[name] = wolTarget{mac: mac, broadcast: broadcast}
	}
	return targets, nil
}

// parseBroadcast turns a broadcast setting into an IPv4 address and port. The
// port defaults to 9. Wake-on-LAN goes out over IPv4, so an IPv6 address or a
// bad port is an error at startup instead of a failure at send time.
func parseBroadcast(s string) (netip.AddrPort, error) {
	if addr, err := netip.ParseAddr(s); err == nil {
		if !addr.Is4() {
			return netip.AddrPort{}, fmt.Errorf("broadcast address %q is not IPv4", s)
		}
		return netip.AddrPortFrom(addr, 9), nil
	}
	addrPort, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("broadcast address %q is not an IPv4 address, optionally with a port: %w", s, err)
	}
	if !addrPort.Addr().Is4() {
		return netip.AddrPort{}, fmt.Errorf("broadcast address %q is not IPv4", s)
	}
	return addrPort, nil
}

// sendMagicPacket sends a Wake-on-LAN packet to dst, normally the target
// network's broadcast address.
func sendMagicPacket(mac macAddr, dst netip.AddrPort) (err error) {
	var packet [6 + len(mac)*16]byte
	for i := range 6 {
		packet[i] = 0xff
	}
	for i := 6; i < len(packet); i += len(mac) {
		copy(packet[i:], mac[:])
	}

	conn, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(dst))
	if err != nil {
		return fmt.Errorf("open Wake-on-LAN connection: %w", err)
	}
	defer func() {
		if e := conn.Close(); e != nil {
			err = errors.Join(err, fmt.Errorf("connection was not close gracefully: %w", e))
		}
	}()

	if _, err = conn.Write(packet[:]); err != nil {
		return fmt.Errorf("send Wake-on-LAN packet: %w", err)
	}
	return
}

func interactionAuthor(i *discordgo.Interaction) *discordgo.User {
	if i.Member != nil {
		return i.Member.User
	}
	return i.User
}

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content},
	})
	if err != nil {
		log.Printf("could not respond to interaction: %s", err)
	}
}

var wolTargets map[string]wolTarget

func main() {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	appID := os.Getenv("DISCORD_APP_ID")
	guildID := os.Getenv("DISCORD_GUILD_ID")
	targetsSpec := os.Getenv("WOL_TARGETS")

	if token == "" {
		log.Fatal("DISCORD_BOT_TOKEN must be set!")
	}
	if appID == "" {
		log.Fatal("DISCORD_APP_ID must be set!")
	}
	if targetsSpec == "" {
		log.Fatal("WOL_TARGETS must be set!")
	}

	targets, err := parseWOLTargets(targetsSpec)
	if err != nil {
		log.Fatalf("Invalid WOL_TARGETS: %s", err)
	}
	wolTargets = targets

	commands, err := buildCommands([]commandSpec{echoCommand, wolCommand})
	if err != nil {
		log.Fatalf("Invalid commands: %s", err)
	}

	session, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Failed to create a new Discord session: %s", err)
	}

	session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			commands.handleCommand(s, i)
		case discordgo.InteractionApplicationCommandAutocomplete:
			commands.handleAutocomplete(s, i)
		}
	})

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as %s", r.User.String())
	})

	_, err = session.ApplicationCommandBulkOverwrite(appID, guildID, commands.definitions)
	if err != nil {
		log.Fatalf("Could not register commands: %s", err)
	}

	if err = session.Open(); err != nil {
		log.Fatalf("Could not open session: %s", err)
	}

	sigch := make(chan os.Signal, 1)
	signal.Notify(sigch, os.Interrupt)
	<-sigch

	if err = session.Close(); err != nil {
		log.Printf("Could not close session gracefully: %s", err)
	}
}
