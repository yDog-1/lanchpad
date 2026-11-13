package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func commandInteraction(kind discordgo.InteractionType, name string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction", Token: "token", Type: kind,
		User: &discordgo.User{Username: "tester", Discriminator: "1234"},
		Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: options},
	}}
}

func captureResponse(t *testing.T, run func(*discordgo.Session)) discordgo.InteractionResponse {
	t.Helper()
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = &http.Client{Transport: rewriteTransport{base: server.Client().Transport, url: server.URL}}
	run(session)
	var response discordgo.InteractionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode interaction response %q: %v", body, err)
	}
	return response
}

type rewriteTransport struct {
	base http.RoundTripper
	url  string
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme = "http"
	copy.URL.Host = r.url[len("http://"):]
	return r.base.RoundTrip(copy)
}

func testCommands(t *testing.T) *commandRegistry {
	t.Helper()
	commands, err := buildCommands([]commandSpec{echoCommand, wolCommand})
	if err != nil {
		t.Fatal(err)
	}
	return commands
}

func TestPublishedCommands(t *testing.T) {
	want := []*discordgo.ApplicationCommand{
		{Name: "echo", Description: "Say something through a bot", Options: []*discordgo.ApplicationCommandOption{
			{Name: "message", Description: "Contents of the message", Type: discordgo.ApplicationCommandOptionString, Required: true},
			{Name: "author", Description: "Whether to prepend message's author", Type: discordgo.ApplicationCommandOptionBoolean},
		}},
		{Name: "wol", Description: "Wake a machine with Wake-on-LAN", Options: []*discordgo.ApplicationCommandOption{
			{Name: "target", Description: "Machine to wake", Type: discordgo.ApplicationCommandOptionString, Required: true, Autocomplete: true},
		}},
	}
	commands := testCommands(t)
	if !reflect.DeepEqual(commands.definitions, want) {
		t.Fatalf("published commands changed: got %#v, want %#v", commands.definitions, want)
	}
}

func TestCommandResponses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
		options []*discordgo.ApplicationCommandInteractionDataOption
		want    string
	}{
		{"echo", "echo", []*discordgo.ApplicationCommandInteractionDataOption{{Name: "message", Type: discordgo.ApplicationCommandOptionString, Value: "hello"}}, "hello"},
		{"echo with author", "echo", []*discordgo.ApplicationCommandInteractionDataOption{{Name: "message", Type: discordgo.ApplicationCommandOptionString, Value: "hello"}, {Name: "author", Type: discordgo.ApplicationCommandOptionBoolean, Value: true}}, "**tester#1234** says: hello"},
		{"unknown WOL target", "wol", []*discordgo.ApplicationCommandInteractionDataOption{{Name: "target", Type: discordgo.ApplicationCommandOptionString, Value: "missing"}}, "Unknown Wake-on-LAN target: missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := captureResponse(t, func(s *discordgo.Session) {
				testCommands(t).handleCommand(s, commandInteraction(discordgo.InteractionApplicationCommand, tc.command, tc.options...))
			})
			if response.Type != discordgo.InteractionResponseChannelMessageWithSource || response.Data == nil || response.Data.Content != tc.want {
				t.Fatalf("unexpected response: %#v", response)
			}
		})
	}
}

func TestWolCommandSendsPacket(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	mac := mustParseMAC(t, "00:11:22:33:44:55")
	previous := wolTargets
	wolTargets = map[string]wolTarget{"desktop": {mac: mac, broadcast: listener.LocalAddr().(*net.UDPAddr).AddrPort()}}
	t.Cleanup(func() { wolTargets = previous })
	response := captureResponse(t, func(s *discordgo.Session) {
		testCommands(t).handleCommand(s, commandInteraction(discordgo.InteractionApplicationCommand, "wol",
			&discordgo.ApplicationCommandInteractionDataOption{Name: "target", Type: discordgo.ApplicationCommandOptionString, Value: "desktop"}))
	})
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource || response.Data == nil || response.Data.Content != "Sent a magic packet to **desktop**." {
		t.Fatalf("unexpected WOL response: %#v", response)
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

func TestBuildCommandsErrors(t *testing.T) {
	handler := func(*discordgo.Session, *discordgo.InteractionCreate, optionMap) {}
	valid := commandSpec{Definition: &discordgo.ApplicationCommand{Name: "test"}, Execute: handler}
	for _, tc := range []struct {
		name  string
		specs []commandSpec
		want  string
	}{
		{"missing definition", []commandSpec{{Execute: handler}}, "command definition is missing"},
		{"empty name", []commandSpec{{Definition: &discordgo.ApplicationCommand{}, Execute: handler}}, "command name is empty"},
		{"missing handler", []commandSpec{{Definition: &discordgo.ApplicationCommand{Name: "test"}}}, "command \"test\" has no execution handler"},
		{"duplicate name", []commandSpec{valid, valid}, "command \"test\" is defined twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildCommands(tc.specs)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCommandRegistryDispatch(t *testing.T) {
	for _, name := range []string{"original", "renamed"} {
		t.Run(name, func(t *testing.T) {
			var executions, completions int
			commands, err := buildCommands([]commandSpec{
				{
					Definition: &discordgo.ApplicationCommand{Name: name},
					Execute: func(_ *discordgo.Session, _ *discordgo.InteractionCreate, opts optionMap) {
						executions++
						if opts["message"].StringValue() != "hello" {
							t.Error("message option was not passed to execution handler")
						}
					},
					Autocomplete: func(_ *discordgo.Session, i *discordgo.InteractionCreate) {
						completions++
						if i.ApplicationCommandData().Name != name {
							t.Error("autocomplete received a different interaction")
						}
					},
				},
				{Definition: &discordgo.ApplicationCommand{Name: "no-autocomplete"}, Execute: func(*discordgo.Session, *discordgo.InteractionCreate, optionMap) {
					t.Error("unrelated execution handler called")
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(commands.definitions) != 2 || commands.definitions[0].Name != name || commands.definitions[1].Name != "no-autocomplete" {
				t.Fatal("registration names or order changed")
			}
			commands.handleCommand(nil, commandInteraction(discordgo.InteractionApplicationCommand, name,
				&discordgo.ApplicationCommandInteractionDataOption{Name: "message", Type: discordgo.ApplicationCommandOptionString, Value: "hello"}))
			if executions != 1 || completions != 0 {
				t.Fatalf("after execution: executions=%d completions=%d", executions, completions)
			}
			commands.handleAutocomplete(nil, commandInteraction(discordgo.InteractionApplicationCommandAutocomplete, name))
			commands.handleCommand(nil, commandInteraction(discordgo.InteractionApplicationCommand, "unknown"))
			commands.handleAutocomplete(nil, commandInteraction(discordgo.InteractionApplicationCommandAutocomplete, "unknown"))
			commands.handleAutocomplete(nil, commandInteraction(discordgo.InteractionApplicationCommandAutocomplete, "no-autocomplete"))
			if executions != 1 || completions != 1 {
				t.Fatalf("after autocomplete and unknown commands: executions=%d completions=%d", executions, completions)
			}
		})
	}
}

func TestAutocompleteResponse(t *testing.T) {
	previous := wolTargets
	wolTargets = map[string]wolTarget{"desktop": {}, "nas": {}}
	t.Cleanup(func() { wolTargets = previous })
	response := captureResponse(t, func(s *discordgo.Session) {
		testCommands(t).handleAutocomplete(s, commandInteraction(discordgo.InteractionApplicationCommandAutocomplete, "wol",
			&discordgo.ApplicationCommandInteractionDataOption{Name: "target", Type: discordgo.ApplicationCommandOptionString, Value: "desk", Focused: true}))
	})
	want := []*discordgo.ApplicationCommandOptionChoice{{Name: "desktop", Value: "desktop"}}
	if response.Type != discordgo.InteractionApplicationCommandAutocompleteResult || response.Data == nil || !reflect.DeepEqual(response.Data.Choices, want) {
		t.Fatalf("unexpected autocomplete response: %#v", response)
	}
}
