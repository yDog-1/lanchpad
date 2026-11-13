package main

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

var echoCommand = commandSpec{
	Definition: &discordgo.ApplicationCommand{
		Name:        "echo",
		Description: "Say something through a bot",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "message",
				Description: "Contents of the message",
				Type:        discordgo.ApplicationCommandOptionString,
				Required:    true,
			},
			{
				Name:        "author",
				Description: "Whether to prepend message's author",
				Type:        discordgo.ApplicationCommandOptionBoolean,
			},
		},
	},
	Execute: handleEcho,
}

func handleEcho(s *discordgo.Session, i *discordgo.InteractionCreate, opts optionMap) {
	builder := new(strings.Builder)
	if v, ok := opts["author"]; ok && v.BoolValue() {
		author := interactionAuthor(i.Interaction)
		builder.WriteString("**")
		builder.WriteString(author.String())
		builder.WriteString("** says: ")
	}
	builder.WriteString(opts["message"].StringValue())

	log.Print("Start send message")
	respond(s, i, builder.String())
}
