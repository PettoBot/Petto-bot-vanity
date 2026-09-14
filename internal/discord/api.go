package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/PettoBot/vanity-tag-bot/internal/profile"
	"github.com/bwmarrin/discordgo"
)

type roleAPI struct{ session *discordgo.Session }

func (r roleAPI) AddRole(ctx context.Context, guildID, userID, roleID string) error {
	return r.session.GuildMemberRoleAdd(guildID, userID, roleID, discordgo.WithContext(ctx))
}

func (r roleAPI) RemoveRole(ctx context.Context, guildID, userID, roleID string) error {
	return r.session.GuildMemberRoleRemove(guildID, userID, roleID, discordgo.WithContext(ctx))
}

type profileAPI struct{ session *discordgo.Session }

func (p profileAPI) ModifyCurrentMember(ctx context.Context, guildID string, update profile.MemberUpdate) error {
	payload := map[string]interface{}{}
	if update.Nickname != nil {
		if *update.Nickname == "" {
			payload["nick"] = nil
		} else {
			payload["nick"] = *update.Nickname
		}
	}
	if update.Avatar != nil {
		if *update.Avatar == "" {
			payload["avatar"] = nil
		} else {
			payload["avatar"] = *update.Avatar
		}
	}
	if update.Banner != nil {
		if *update.Banner == "" {
			payload["banner"] = nil
		} else {
			payload["banner"] = *update.Banner
		}
	}
	if update.Bio != nil {
		if *update.Bio == "" {
			payload["bio"] = nil
		} else {
			payload["bio"] = *update.Bio
		}
	}
	if len(payload) == 0 {
		return nil
	}
	endpoint := discordgo.EndpointGuildMember(guildID, "@me")
	_, err := p.session.Request(http.MethodPatch, endpoint, payload, discordgo.WithContext(ctx))
	return err
}

// rawUserUpdate is used because discordgo v0.29.0's User struct predates the
// primary_guild field. The regular typed USER_UPDATE handler is still useful
// for cache updates; this decoder preserves the new identity payload.
type rawUserUpdate struct {
	ID           string           `json:"id"`
	Username     string           `json:"username"`
	GlobalName   string           `json:"global_name"`
	PrimaryGuild *rawPrimaryGuild `json:"primary_guild"`
}

type rawPrimaryGuild struct {
	IdentityGuildID string `json:"identity_guild_id"`
	IdentityEnabled *bool  `json:"identity_enabled"`
	Tag             string `json:"tag"`
	Badge           string `json:"badge"`
}

func decodePrimaryGuild(data []byte) (rawUserUpdate, error) {
	var payload rawUserUpdate
	if err := json.Unmarshal(data, &payload); err != nil {
		return rawUserUpdate{}, fmt.Errorf("decode USER_UPDATE: %w", err)
	}
	return payload, nil
}
