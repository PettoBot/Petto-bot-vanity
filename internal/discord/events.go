package discord

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/bwmarrin/discordgo"
)

func (b *Bot) registerEventHandlers() {
	b.session.AddHandler(func(session *discordgo.Session, _ *discordgo.Ready) {
		presence := discordgo.UpdateStatusData{Status: "online", AFK: false, Activities: []*discordgo.Activity{}}
		if b.config.PresenceText != "" {
			presence.Activities = []*discordgo.Activity{{Name: "Custom Status", Type: discordgo.ActivityTypeCustom, State: b.config.PresenceText}}
		}
		if err := session.UpdateStatusComplex(presence); err != nil {
			b.logger.Warn("set Discord presence failed", "error", err, "shard_id", session.ShardID)
			return
		}
		b.logger.Info("Discord gateway ready", "shard_id", session.ShardID, "shard_count", session.ShardCount, "presence", b.config.PresenceText, "status", "online")
	})
	b.session.AddHandler(func(_ *discordgo.Session, event *discordgo.GuildCreate) {
		if event == nil || event.Guild == nil {
			return
		}
		ctx, cancel := b.operationContext()
		defer cancel()
		if err := b.store.EnsureGuild(ctx, event.Guild.ID); err != nil {
			b.logger.Error("ensure guild", "error", err)
		}
	})
	b.session.AddHandler(func(_ *discordgo.Session, event *discordgo.GuildMemberAdd) {
		if event == nil || event.Member == nil {
			return
		}
		if event.Member.User != nil && event.Member.User.Bot {
			return
		}
		go b.evaluateMember(event.GuildID, b.cachedMemberIdentity(event.GuildID, event.Member, nil))
	})
	b.session.AddHandler(func(_ *discordgo.Session, event *discordgo.GuildMemberUpdate) {
		if event == nil || event.Member == nil {
			return
		}
		if event.Member.User != nil && event.Member.User.Bot {
			return
		}
		go b.evaluateMember(event.GuildID, b.cachedMemberIdentity(event.GuildID, event.Member, nil))
	})
	b.session.AddHandler(func(session *discordgo.Session, event *discordgo.PresenceUpdate) {
		if event == nil || event.GuildID == "" || event.User == nil || event.User.Bot {
			return
		}
		var member *discordgo.Member
		if session != nil && session.State != nil {
			member, _ = session.State.Member(event.GuildID, event.User.ID)
		}
		if member == nil {
			member = &discordgo.Member{GuildID: event.GuildID, User: event.User}
		}
		resolved := memberIdentity(event.GuildID, member, nil)
		resolved.CustomStatus = customStatusValue(&event.Presence)
		resolved.UnknownVanitySources = nil
		go b.evaluateMemberForSource(event.GuildID, resolved, identity.SourceVanity)
	})
	b.session.AddHandler(func(_ *discordgo.Session, event *discordgo.GuildMemberRemove) {
		if event == nil || event.Member == nil {
			return
		}
		// A removed member cannot be reconciled through Discord. Grants remain
		// auditable and are made inactive by the next explicit sync/rejoin.
		userID := "unknown"
		if event.User != nil {
			userID = event.User.ID
		}
		b.logger.Info("member left; retained role ledger for audit", "guild_id", event.GuildID, "user_id", userID)
	})
	b.session.AddHandler(func(_ *discordgo.Session, event *discordgo.Event) {
		if event == nil || event.Type != "USER_UPDATE" {
			return
		}
		payload, err := decodePrimaryGuild(event.RawData)
		if err != nil {
			b.logger.Warn("decode user update", "error", err)
			return
		}
		if payload.ID == "" {
			return
		}
		primary := (*identity.PrimaryGuild)(nil)
		if payload.PrimaryGuild != nil {
			primary = &identity.PrimaryGuild{IdentityGuildID: payload.PrimaryGuild.IdentityGuildID, IdentityEnabled: payload.PrimaryGuild.IdentityEnabled, Tag: payload.PrimaryGuild.Tag, Badge: payload.PrimaryGuild.Badge}
		}
		go b.evaluateUserUpdate(payload, primary)
	})
	b.session.AddHandler(func(_ *discordgo.Session, event *discordgo.InteractionCreate) {
		b.handleInteraction(event)
	})
}

func (b *Bot) evaluateMember(guildID string, member identity.MemberIdentity) {
	b.evaluateMemberForSource(guildID, member, "")
}

func (b *Bot) evaluateMemberForSource(guildID string, member identity.MemberIdentity, source identity.Source) {
	b.withIdentityLock(guildID, member.UserID, func() {
		// Each external operation gets its own timeout. In particular, a slow
		// Discord /users/:id request for primary_guild must never consume the
		// context later used for PostgreSQL or Vanity evaluation.
		var vanityRules []identity.VanityRule
		var tagRules []identity.GuildTagRule

		if source != identity.SourceGuildTag {
			ctx, cancel := b.operationContext()
			rules, err := b.store.ListVanityRules(ctx, guildID)
			cancel()
			if err != nil {
				b.logger.Error("load vanity rules before member evaluation failed", "guild_id", guildID, "user_id", member.UserID, "error", err)
				return
			}
			vanityRules = rules
		}

		if source != identity.SourceVanity {
			ctx, cancel := b.operationContext()
			rules, err := b.store.ListGuildTagRules(ctx, guildID)
			cancel()
			if err != nil {
				b.logger.Error("load guild tag rules before member evaluation failed", "guild_id", guildID, "user_id", member.UserID, "error", err)
				return
			}
			tagRules = rules

			if len(tagRules) > 0 && member.PrimaryGuild == nil && member.UserID != "" {
				primaryCtx, primaryCancel := b.operationContext()
				primary, loadErr := b.loadPrimaryGuild(primaryCtx, member.UserID)
				primaryCancel()
				if loadErr != nil {
					b.logger.Warn("load member primary_guild failed; skipping guild tag evaluation", "guild_id", guildID, "user_id", member.UserID, "error", loadErr)
					// nil tells the engine that this source was intentionally not
					// evaluated. Existing Guild Tag grants remain untouched until
					// Discord provides authoritative primary_guild data again.
					tagRules = nil
				} else if primary == nil {
					// Discord omitted primary_guild. That means unknown, not
					// identity_disabled and not a negative comparison match.
					tagRules = nil
				} else {
					member.PrimaryGuild = primary
				}
			}
		}

		evalCtx, evalCancel := b.operationContext()
		defer evalCancel()
		if _, err := b.identity.Engine.Evaluate(evalCtx, member, vanityRules, tagRules); err != nil {
			b.logger.Error("evaluate member", "guild_id", guildID, "user_id", member.UserID, "error", err)
		}
	})
}

func (b *Bot) evaluateUserUpdate(payload rawUserUpdate, primary *identity.PrimaryGuild) {
	// USER_UPDATE has no guild id. Only cached memberships and guilds with
	// active rules are considered; no global member polling is triggered.
	if b.session.State.User != nil && payload.ID == b.session.State.User.ID {
		return
	}
	for _, guild := range b.session.State.Guilds {
		if guild == nil {
			continue
		}
		checkCtx, cancel := b.operationContext()
		relevant, err := b.store.HasActiveRules(checkCtx, guild.ID)
		cancel()
		if err != nil || !relevant {
			continue
		}
		member, err := b.session.State.Member(guild.ID, payload.ID)
		if err != nil || member == nil {
			continue
		}
		if member.User == nil || member.User.Bot {
			continue
		}
		resolved := b.cachedMemberIdentity(guild.ID, member, primary)
		if payload.Username != "" {
			resolved.Username = payload.Username
		}
		if payload.GlobalName != "" {
			resolved.GlobalName, resolved.DisplayName = payload.GlobalName, payload.GlobalName
		}
		go b.evaluateMember(guild.ID, resolved)
	}
}

func memberIdentity(guildID string, member *discordgo.Member, primary *identity.PrimaryGuild) identity.MemberIdentity {
	result := identity.MemberIdentity{GuildID: guildID, RoleIDs: make(map[string]struct{}), PrimaryGuild: primary}
	if member == nil {
		return result
	}
	result.GuildNickname = member.Nick
	if member.User != nil {
		result.UserID, result.IsBot, result.Username, result.GlobalName = member.User.ID, member.User.Bot, member.User.Username, member.User.GlobalName
		result.DisplayName = member.User.DisplayName()
	}
	for _, roleID := range member.Roles {
		result.RoleIDs[roleID] = struct{}{}
	}
	return result
}

func (b *Bot) cachedMemberIdentity(guildID string, member *discordgo.Member, primary *identity.PrimaryGuild) identity.MemberIdentity {
	result := memberIdentity(guildID, member, primary)
	// Guild member REST payloads do not contain presence/custom-status data.
	// Mark Custom Status unknown until State proves it has an authoritative
	// presence snapshot. This prevents manual sync from treating "not cached"
	// as an empty status and removing a role incorrectly.
	result.UnknownVanitySources = map[identity.VanitySource]struct{}{identity.VanityCustomStatus: {}}
	if member == nil || member.User == nil || b.session == nil || b.session.State == nil {
		return result
	}
	if presence, err := b.session.State.Presence(guildID, member.User.ID); err == nil {
		result.CustomStatus = customStatusValue(presence)
		delete(result.UnknownVanitySources, identity.VanityCustomStatus)
	}
	return result
}

func customStatusValue(presence *discordgo.Presence) string {
	if presence == nil {
		return ""
	}
	for _, activity := range presence.Activities {
		if activity == nil || activity.Type != discordgo.ActivityTypeCustom {
			continue
		}
		if value := strings.TrimSpace(activity.State); value != "" {
			return value
		}
		return strings.TrimSpace(activity.Emoji.Name)
	}
	return ""
}

func (b *Bot) withGuildLock(guildID string, action func()) {
	b.locksMu.Lock()
	lock, ok := b.guildLocks[guildID]
	if !ok {
		lock = &sync.Mutex{}
		b.guildLocks[guildID] = lock
	}
	b.locksMu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	action()
}

func (b *Bot) withIdentityLock(guildID, userID string, action func()) {
	// A fixed set of striped locks serializes evaluations for the same member
	// without forcing every member in a guild through one global mutex. This
	// keeps manual sync concurrent while still preventing duplicate role
	// mutations when Gateway events and a sync hit the same user together.
	const offset32 = uint32(2166136261)
	const prime32 = uint32(16777619)
	hash := offset32
	for _, value := range []byte(guildID + ":" + userID) {
		hash ^= uint32(value)
		hash *= prime32
	}
	lock := &b.identityLocks[hash%uint32(len(b.identityLocks))]
	lock.Lock()
	defer lock.Unlock()
	action()
}

func (b *Bot) operationContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), b.requestTimeout)
}

func (b *Bot) startReconciler(ctx context.Context) {
	if !b.enableReconcile {
		return
	}
	go func() {
		ticker := time.NewTicker(b.reconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.reconcileOnce(ctx)
			}
		}
	}()
}

func (b *Bot) reconcileOnce(ctx context.Context) {
	jobs := make(chan *discordgo.Member)
	var workers sync.WaitGroup
	for index := 0; index < b.reconcileConcurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for member := range jobs {
				if member != nil && member.User != nil && !member.User.Bot {
					b.evaluateMember(member.GuildID, b.cachedMemberIdentity(member.GuildID, member, nil))
				}
			}
		}()
	}
	for _, guild := range b.session.State.Guilds {
		if guild == nil {
			continue
		}
		checkCtx, cancel := b.operationContext()
		relevant, err := b.store.HasActiveRules(checkCtx, guild.ID)
		cancel()
		if err != nil || !relevant {
			continue
		}
		limit := boundedMemberLimit(b.reconcileMaxUsers)
		members, err := b.fetchGuildMembersPaginated(ctx, guild.ID, limit)
		if err != nil {
			b.logger.Warn("reconciliation member fetch failed", "guild_id", guild.ID, "error", err)
			continue
		}
		for _, member := range members {
			select {
			case jobs <- member:
			case <-ctx.Done():
				close(jobs)
				workers.Wait()
				return
			}
		}
	}
	close(jobs)
	workers.Wait()
	b.logger.Info("bounded reconciliation completed", "max_users_per_guild", boundedMemberLimit(b.reconcileMaxUsers), "concurrency", b.reconcileConcurrency)
}

const (
	discordMemberPageSize = 1000
	maxGuildSyncMembers   = 10000
)

func boundedMemberLimit(limit int) int {
	if limit < 1 {
		return 1
	}
	if limit > maxGuildSyncMembers {
		return maxGuildSyncMembers
	}
	return limit
}

func (b *Bot) fetchGuildMembersPaginated(parent context.Context, guildID string, limit int) ([]*discordgo.Member, error) {
	limit = boundedMemberLimit(limit)
	result := make([]*discordgo.Member, 0, limit)
	after := ""
	for len(result) < limit {
		pageSize := discordMemberPageSize
		if remaining := limit - len(result); remaining < pageSize {
			pageSize = remaining
		}
		requestCtx, cancel := context.WithTimeout(parent, b.requestTimeout)
		page, err := b.session.GuildMembers(guildID, after, pageSize, discordgo.WithContext(requestCtx))
		cancel()
		if err != nil {
			return result, err
		}
		if len(page) == 0 {
			break
		}
		result = append(result, page...)
		if len(page) < pageSize {
			break
		}
		last := page[len(page)-1]
		if last == nil || last.User == nil || last.User.ID == "" || last.User.ID == after {
			break
		}
		after = last.User.ID
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

var _ = json.Valid
var _ = slog.LevelInfo
