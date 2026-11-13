package main

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
)

var wolCommand = commandSpec{
	Definition: &discordgo.ApplicationCommand{
		Name:        "wol",
		Description: "Wake a machine with Wake-on-LAN",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:         "target",
				Description:  "Machine to wake",
				Type:         discordgo.ApplicationCommandOptionString,
				Required:     true,
				Autocomplete: true,
			},
		},
	},
	Execute:      handleWol,
	Autocomplete: handleWolAutocomplete,
}

func handleWol(s *discordgo.Session, i *discordgo.InteractionCreate, opts optionMap) {
	name := opts["target"].StringValue()
	target, ok := wolTargets[name]
	if !ok {
		respond(s, i, fmt.Sprintf("Unknown Wake-on-LAN target: %s", name))
		return
	}
	if err := sendMagicPacket(target.mac, target.broadcast); err != nil {
		log.Printf("could not wake %s: %s", name, err)
		respond(s, i, fmt.Sprintf("Could not send a magic packet to %s.", name))
		return
	}
	log.Printf("Sent a magic packet to %s", name)
	respond(s, i, fmt.Sprintf("Sent a magic packet to **%s**.", name))
}

const maxTargetChoices = 25

// targetChoices lists the targets matching typed, for Discord's autocomplete.
// Discord rejects more than 25 choices.
func targetChoices(targets map[string]wolTarget, typed string) []*discordgo.ApplicationCommandOptionChoice {
	typed = strings.ToLower(typed)
	names := make([]string, 0, len(targets))
	for name := range targets {
		if strings.Contains(strings.ToLower(name), typed) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > maxTargetChoices {
		names = names[:maxTargetChoices]
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(names))
	for _, name := range names {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name})
	}
	return choices
}

func handleWolAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	var typed string
	if len(data.Options) > 0 && data.Options[0].Focused {
		typed = data.Options[0].StringValue()
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{
			Choices: targetChoices(wolTargets, typed),
		},
	})
	if err != nil {
		log.Printf("could not respond to autocomplete: %s", err)
	}
}
