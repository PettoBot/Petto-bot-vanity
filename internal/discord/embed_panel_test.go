package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestEmbedModalDefersMessageUpdate(t *testing.T) {
	if got := embedModalDeferType(); got != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("embed modal defer type = %v, want DeferredMessageUpdate", got)
	}
}
