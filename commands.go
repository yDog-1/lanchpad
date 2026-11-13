package main

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

type optionMap = map[string]*discordgo.ApplicationCommandInteractionDataOption

type commandSpec struct {
	Definition   *discordgo.ApplicationCommand
	Execute      func(*discordgo.Session, *discordgo.InteractionCreate, optionMap)
	Autocomplete func(*discordgo.Session, *discordgo.InteractionCreate)
}

type commandRegistry struct {
	definitions []*discordgo.ApplicationCommand
	byName      map[string]commandSpec
}

func buildCommands(specs []commandSpec) (*commandRegistry, error) {
	registry := &commandRegistry{
		definitions: make([]*discordgo.ApplicationCommand, 0, len(specs)),
		byName:      make(map[string]commandSpec, len(specs)),
	}
	for _, spec := range specs {
		if spec.Definition == nil {
			return nil, fmt.Errorf("command definition is missing")
		}
		name := spec.Definition.Name
		if name == "" {
			return nil, fmt.Errorf("command name is empty")
		}
		if _, exists := registry.byName[name]; exists {
			return nil, fmt.Errorf("command %q is defined twice", name)
		}
		if spec.Execute == nil {
			return nil, fmt.Errorf("command %q has no execution handler", name)
		}
		registry.definitions = append(registry.definitions, spec.Definition)
		registry.byName[name] = spec
	}
	return registry, nil
}

func parseOptions(options []*discordgo.ApplicationCommandInteractionDataOption) optionMap {
	opts := make(optionMap, len(options))
	for _, opt := range options {
		opts[opt.Name] = opt
	}
	return opts
}

func (r *commandRegistry) handleCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if command, ok := r.byName[data.Name]; ok {
		command.Execute(s, i, parseOptions(data.Options))
	}
}

func (r *commandRegistry) handleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if command, ok := r.byName[data.Name]; ok && command.Autocomplete != nil {
		command.Autocomplete(s, i)
	}
}
