package discord

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/bwmarrin/discordgo"
)

const (
	syncProgressSegments     = 8
	syncProgressEditEvery    = 1250 * time.Millisecond
	maxManualSyncWorkers     = 8
	maxManualSyncQueueDepth  = 256
	primaryCircuitThreshold  = 4
	primaryCircuitOpenFor    = 30 * time.Second
	maxSyncPrimaryLookupTime = 5 * time.Second

	syncStartFull   = "<:petto_iniciolleno:1534705766370381885>"
	syncStartHalf   = "<:petto_iniciomediolleno:1534705752692883486>"
	syncStartEmpty  = "<:petto_iniciovavio:1534705756496990259>"
	syncMiddleFull  = "<:petto_lleno:1534705778613682378>"
	syncMiddleHalf  = "<:petto_mediolleno:1534705751220555777>"
	syncMiddleEmpty = "<:petto_vacio:1534705753850380348>"
	syncEndFull     = "<:petto_finallleno:1534705777099673822>"
	syncEndHalf     = "<:petto_finalmediolleno:1534705779540627568>"
	syncEndEmpty    = "<:petto_finalvacio:1534705754852819014>"
)

type manualSyncStats struct {
	StartedAt       time.Time
	Total           int
	Limit           int
	Processed       int
	Evaluated       int
	SkippedBots     int
	UnknownIdentity int
	UnknownVanity   int
	RoleDecisions   int
	RolesAdded      int
	RolesRemoved    int
	ManualProtected int
	Warnings        int
	Errors          int
	Limited         bool
	FatalError      string
	LastWarning     string
}

type syncMemberResult struct {
	evaluated       bool
	unknownIdentity bool
	unknownVanity   bool
	roleDecisions   int
	rolesAdded      int
	rolesRemoved    int
	manualProtected int
	warnings        int
	errors          int
	warning         string
}

type primaryLookupCircuit struct {
	mu                  sync.Mutex
	consecutiveFailures int
	openUntil           time.Time
}

func (c *primaryLookupCircuit) allow(now time.Time) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openUntil.IsZero() || !now.Before(c.openUntil)
}

func (c *primaryLookupCircuit) success() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.consecutiveFailures = 0
	c.openUntil = time.Time{}
	c.mu.Unlock()
}

func (c *primaryLookupCircuit) failure(now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.consecutiveFailures++
	if c.consecutiveFailures >= primaryCircuitThreshold {
		c.openUntil = now.Add(primaryCircuitOpenFor)
	}
	c.mu.Unlock()
}

func (s *manualSyncStats) apply(result syncMemberResult) {
	s.Processed++
	if result.evaluated {
		s.Evaluated++
	}
	if result.unknownIdentity {
		s.UnknownIdentity++
	}
	if result.unknownVanity {
		s.UnknownVanity++
	}
	s.RoleDecisions += result.roleDecisions
	s.RolesAdded += result.rolesAdded
	s.RolesRemoved += result.rolesRemoved
	s.ManualProtected += result.manualProtected
	s.Warnings += result.warnings
	s.Errors += result.errors
	if result.warning != "" {
		s.LastWarning = sanitizeSyncMessage(result.warning)
	}
}

func (b *Bot) claimManualSync(guildID string, source identity.Source) bool {
	key := guildID + ":" + string(source)
	b.manualSyncMu.Lock()
	defer b.manualSyncMu.Unlock()
	if b.manualSyncRunning == nil {
		b.manualSyncRunning = make(map[string]struct{})
	}
	if _, exists := b.manualSyncRunning[key]; exists {
		return false
	}
	b.manualSyncRunning[key] = struct{}{}
	return true
}

func (b *Bot) releaseManualSync(guildID string, source identity.Source) {
	key := guildID + ":" + string(source)
	b.manualSyncMu.Lock()
	delete(b.manualSyncRunning, key)
	b.manualSyncMu.Unlock()
}

func (b *Bot) startManualSync(event *discordgo.InteractionCreate, source identity.Source, userID string) {
	if event == nil || event.Interaction == nil || event.GuildID == "" {
		return
	}
	if !b.claimManualSync(event.GuildID, source) {
		respond(event, fmt.Sprintf("A %s sync is already running for this server. Wait for its private progress panel to finish before starting another one.", notificationSourceName(source)), true)
		return
	}
	stats := manualSyncStats{StartedAt: time.Now()}
	stage := "Preparing synchronization…"
	if userID != "" {
		stats.Total = 1
		stage = "Loading member…"
	}
	data := &discordgo.InteractionResponseData{
		Flags:  discordgo.MessageFlagsEphemeral,
		Embeds: []*discordgo.MessageEmbed{syncProgressEmbed(source, stage, stats, b.manualSyncConcurrency(), false)},
	}
	if err := eventInteractionRespond(b.session, event, data); err != nil {
		b.releaseManualSync(event.GuildID, source)
		b.logger.Error("start manual sync response failed", "guild_id", event.GuildID, "source", source, "error", err)
		return
	}
	interaction := event.Interaction
	guildID := event.GuildID
	go b.runManualSync(interaction, guildID, source, userID)
}

func (b *Bot) runManualSync(interaction *discordgo.Interaction, guildID string, source identity.Source, userID string) {
	defer b.releaseManualSync(guildID, source)
	stats := manualSyncStats{StartedAt: time.Now()}
	workers := b.manualSyncConcurrency()

	vanityRules, tagRules, err := b.loadManualSyncRules(guildID, source)
	if err != nil {
		stats.FatalError = "Could not load rules: " + sanitizeSyncMessage(err.Error())
		stats.Errors++
		b.editManualSync(interaction, source, "Synchronization failed", stats, workers, true)
		return
	}

	if userID != "" {
		stats.Total = 1
		b.editManualSync(interaction, source, "Loading member…", stats, 1, false)
		memberCtx, cancel := b.operationContext()
		member, memberErr := b.session.GuildMember(guildID, userID, discordgo.WithContext(memberCtx))
		cancel()
		if memberErr != nil {
			stats.Processed = 1
			stats.Errors = 1
			stats.FatalError = "Could not load the selected member: " + sanitizeSyncMessage(memberErr.Error())
			b.editManualSync(interaction, source, "Synchronization failed", stats, 1, true)
			return
		}
		if member == nil || member.User == nil || member.User.Bot {
			stats.Processed = 1
			stats.SkippedBots = 1
			stats.LastWarning = "Bot accounts are ignored."
			stats.Warnings = 1
			b.editManualSync(interaction, source, "Completed with warnings", stats, 1, true)
			return
		}
		circuit := &primaryLookupCircuit{}
		result := b.syncMemberWithSnapshot(guildID, member, source, vanityRules, tagRules, circuit)
		stats.apply(result)
		b.editManualSync(interaction, source, finalSyncStage(stats), stats, 1, true)
		return
	}

	limit := b.manualSyncMemberLimit()
	stats.Limit = limit
	b.editManualSync(interaction, source, fmt.Sprintf("Fetching members (limit %d)…", limit), stats, workers, false)
	fetchLimit := limit
	if limit < maxGuildSyncMembers {
		fetchLimit++ // fetch one extra member so the UI can report a real bound hit
	}
	members, err := b.fetchGuildMembersPaginated(context.Background(), guildID, fetchLimit)
	if err != nil {
		stats.FatalError = "Could not fetch guild members: " + sanitizeSyncMessage(err.Error())
		stats.Errors++
		b.editManualSync(interaction, source, "Synchronization failed", stats, workers, true)
		return
	}

	stats.Limited = len(members) > limit || (limit == maxGuildSyncMembers && len(members) >= limit)
	if len(members) > limit {
		members = members[:limit]
	}
	humans := make([]*discordgo.Member, 0, len(members))
	for _, member := range members {
		if member == nil || member.User == nil || member.User.Bot {
			stats.SkippedBots++
			continue
		}
		humans = append(humans, member)
	}
	stats.Total = len(humans)
	if stats.Total == 0 {
		b.editManualSync(interaction, source, "No human members to synchronize", stats, workers, true)
		return
	}

	b.editManualSync(interaction, source, "Evaluating members…", stats, workers, false)
	queueDepth := b.config.GuildQueueSize
	if queueDepth < 1 {
		queueDepth = 1
	}
	if queueDepth > maxManualSyncQueueDepth {
		queueDepth = maxManualSyncQueueDepth
	}
	jobs := make(chan *discordgo.Member, queueDepth)
	results := make(chan syncMemberResult, workers)
	circuit := &primaryLookupCircuit{}
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for member := range jobs {
				results <- b.syncMemberWithSnapshot(guildID, member, source, vanityRules, tagRules, circuit)
			}
		}()
	}
	go func() {
		for _, member := range humans {
			jobs <- member
		}
		close(jobs)
		group.Wait()
		close(results)
	}()

	lastEdit := time.Now()
	for result := range results {
		stats.apply(result)
		if stats.Processed == stats.Total || time.Since(lastEdit) >= syncProgressEditEvery {
			b.editManualSync(interaction, source, "Evaluating members…", stats, workers, false)
			lastEdit = time.Now()
		}
	}

	b.editManualSync(interaction, source, finalSyncStage(stats), stats, workers, true)
	b.logger.Info("manual guild sync completed",
		"guild_id", guildID,
		"source", source,
		"members", stats.Total,
		"processed", stats.Processed,
		"evaluated", stats.Evaluated,
		"unknown_identity", stats.UnknownIdentity,
		"unknown_vanity", stats.UnknownVanity,
		"roles_added", stats.RolesAdded,
		"roles_removed", stats.RolesRemoved,
		"warnings", stats.Warnings,
		"errors", stats.Errors,
		"limit", limit,
		"concurrency", workers,
	)
}

func (b *Bot) loadMemberForSource(guildID, userID string, source identity.Source) (identity.MemberIdentity, bool, error) {
	memberCtx, cancel := b.operationContext()
	member, err := b.session.GuildMember(guildID, userID, discordgo.WithContext(memberCtx))
	cancel()
	if err != nil {
		return identity.MemberIdentity{}, false, err
	}
	resolved := b.cachedMemberIdentity(guildID, member, nil)
	if source != identity.SourceGuildTag {
		return resolved, true, nil
	}
	primaryCtx, primaryCancel := b.operationContext()
	primary, err := b.loadPrimaryGuild(primaryCtx, userID)
	primaryCancel()
	if err != nil {
		return resolved, false, err
	}
	if primary == nil {
		return resolved, false, nil
	}
	resolved.PrimaryGuild = primary
	return resolved, true, nil
}

func (b *Bot) loadManualSyncRules(guildID string, source identity.Source) ([]identity.VanityRule, []identity.GuildTagRule, error) {
	if source == identity.SourceVanity {
		ctx, cancel := b.operationContext()
		rules, err := b.store.ListVanityRules(ctx, guildID)
		cancel()
		if err != nil {
			return nil, nil, err
		}
		if rules == nil {
			rules = []identity.VanityRule{}
		}
		return rules, nil, nil
	}
	if source == identity.SourceGuildTag {
		ctx, cancel := b.operationContext()
		rules, err := b.store.ListGuildTagRules(ctx, guildID)
		cancel()
		if err != nil {
			return nil, nil, err
		}
		if rules == nil {
			rules = []identity.GuildTagRule{}
		}
		return nil, rules, nil
	}
	return nil, nil, fmt.Errorf("unsupported manual sync source %q", source)
}

func (b *Bot) syncMemberWithSnapshot(guildID string, member *discordgo.Member, source identity.Source, vanityRules []identity.VanityRule, tagRules []identity.GuildTagRule, circuit *primaryLookupCircuit) syncMemberResult {
	if member == nil || member.User == nil || member.User.Bot {
		return syncMemberResult{}
	}
	resolved := b.cachedMemberIdentity(guildID, member, nil)
	unknownVanity := source == identity.SourceVanity && hasVanitySource(vanityRules, identity.VanityCustomStatus) && !identity.VanitySourceKnown(resolved, identity.VanityCustomStatus)
	if source == identity.SourceGuildTag && len(tagRules) > 0 {
		now := time.Now()
		if !circuit.allow(now) {
			return syncMemberResult{
				unknownIdentity: true,
				warnings:        1,
				warning:         "primary_guild lookups were temporarily paused after repeated Discord errors; existing grants were preserved",
			}
		}
		primaryCtx, cancel := context.WithTimeout(context.Background(), b.manualPrimaryLookupTimeout())
		primary, err := b.loadPrimaryGuild(primaryCtx, member.User.ID)
		cancel()
		if err != nil {
			circuit.failure(now)
			return syncMemberResult{
				unknownIdentity: true,
				warnings:        1,
				warning:         fmt.Sprintf("Discord could not load primary_guild for %s: %s", member.User.ID, err),
			}
		}
		circuit.success()
		if primary == nil {
			return syncMemberResult{
				unknownIdentity: true,
				warnings:        1,
				warning:         fmt.Sprintf("Discord omitted primary_guild for %s; existing Guild Tag grants were preserved", member.User.ID),
			}
		}
		resolved.PrimaryGuild = primary
	}

	result := syncMemberResult{evaluated: true, unknownVanity: unknownVanity}
	if unknownVanity {
		result.warnings++
		result.warning = "Custom Status was not cached for this member; Custom Status grants were preserved while other Vanity sources were evaluated"
	}
	var evaluations []identity.Evaluation
	var evaluateErr error
	b.withIdentityLock(guildID, member.User.ID, func() {
		evalCtx, cancel := b.operationContext()
		defer cancel()
		if source == identity.SourceVanity {
			evaluations, evaluateErr = b.identity.Engine.Evaluate(evalCtx, resolved, vanityRules, nil)
		} else {
			evaluations, evaluateErr = b.identity.Engine.Evaluate(evalCtx, resolved, nil, tagRules)
		}
	})
	if evaluateErr != nil {
		result.errors++
		result.warning = evaluateErr.Error()
		return result
	}

	result.roleDecisions = len(evaluations)
	for _, evaluation := range evaluations {
		if evaluation.Error != nil {
			result.errors++
			result.warning = evaluation.Error.Error()
			continue
		}
		if evaluation.Changed {
			if evaluation.Desired {
				result.rolesAdded++
			} else {
				result.rolesRemoved++
			}
			continue
		}
		if evaluation.ManualMarked && !evaluation.Desired {
			result.manualProtected++
		}
	}
	return result
}

func (b *Bot) editManualSync(interaction *discordgo.Interaction, source identity.Source, stage string, stats manualSyncStats, workers int, final bool) {
	if interaction == nil || b.session == nil {
		return
	}
	embeds := []*discordgo.MessageEmbed{syncProgressEmbed(source, stage, stats, workers, final)}
	content := ""
	components := []discordgo.MessageComponent{}
	if _, err := b.session.InteractionResponseEdit(interaction, &discordgo.WebhookEdit{Content: &content, Embeds: &embeds, Components: &components}); err != nil {
		b.logger.Warn("edit manual sync progress failed", "source", source, "stage", stage, "error", err)
	}
}

func syncProgressEmbed(source identity.Source, stage string, stats manualSyncStats, workers int, final bool) *discordgo.MessageEmbed {
	percent := syncPercent(stats.Processed, stats.Total)
	barDone, barTotal := stats.Processed, stats.Total
	if final && stats.Total == 0 && stats.FatalError == "" {
		percent = 100
		barDone, barTotal = 1, 1
	}
	title := "Vanity Sync"
	if source == identity.SourceGuildTag {
		title = "Server Tag Sync"
	}
	color := helpColor
	if final {
		switch {
		case stats.FatalError != "" || stats.Errors > 0:
			color = 0xED4245
		case stats.Warnings > 0 || stats.UnknownIdentity > 0 || stats.UnknownVanity > 0 || stats.Limited:
			color = 0xFEE75C
		default:
			color = 0x57F287
		}
	}

	description := fmt.Sprintf("%s\n**%d%%** · %s", syncProgressBar(barDone, barTotal), percent, stage)
	fields := []*discordgo.MessageEmbedField{
		{Name: "Members", Value: fmt.Sprintf("`%d / %d` processed · `%d` evaluated", stats.Processed, stats.Total, stats.Evaluated), Inline: true},
		{Name: "Role changes", Value: fmt.Sprintf("`+%d` added · `-%d` removed", stats.RolesAdded, stats.RolesRemoved), Inline: true},
		{Name: "Decisions", Value: fmt.Sprintf("`%d` role decisions", stats.RoleDecisions), Inline: true},
	}
	if source == identity.SourceGuildTag {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Identity safety", Value: fmt.Sprintf("`%d` unknown primary_guild · grants preserved", stats.UnknownIdentity), Inline: false})
	}
	if source == identity.SourceVanity && stats.UnknownVanity > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Vanity safety", Value: fmt.Sprintf("`%d` members had no cached Custom Status · those grants were preserved", stats.UnknownVanity), Inline: false})
	}
	if stats.ManualProtected > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Manual roles protected", Value: fmt.Sprintf("`%d` protected role states were left untouched", stats.ManualProtected), Inline: false})
	}
	if stats.SkippedBots > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Ignored", Value: fmt.Sprintf("`%d` bot/invalid members", stats.SkippedBots), Inline: true})
	}
	if stats.Warnings > 0 || stats.Errors > 0 || stats.Limited || stats.FatalError != "" {
		parts := []string{fmt.Sprintf("`%d` warnings · `%d` errors", stats.Warnings, stats.Errors)}
		if stats.Limited {
			if stats.Limit >= maxGuildSyncMembers {
				parts = append(parts, fmt.Sprintf("This run reached the hard safety cap of `%d` members.", maxGuildSyncMembers))
			} else {
				parts = append(parts, "This run reached `MANUAL_SYNC_MAX_USERS`; increase it if the server is larger.")
			}
		}
		if stats.FatalError != "" {
			parts = append(parts, "**Error:** "+sanitizeSyncMessage(stats.FatalError))
		} else if stats.LastWarning != "" {
			parts = append(parts, "Last warning: `"+sanitizeSyncMessage(stats.LastWarning)+"`")
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Warnings", Value: strings.Join(parts, "\n"), Inline: false})
	}

	elapsed := time.Since(stats.StartedAt)
	if stats.StartedAt.IsZero() {
		elapsed = 0
	}
	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Private Petto sync · %s · %d worker(s)", formatSyncDuration(elapsed), workers)},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}
}

func syncProgressBar(done, total int) string {
	if total < 1 {
		total = 1
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	units := int(math.Round((float64(done) / float64(total)) * float64(syncProgressSegments*2)))
	parts := make([]string, syncProgressSegments)
	for index := 0; index < syncProgressSegments; index++ {
		state := 0 // empty
		if units >= (index+1)*2 {
			state = 2 // full
		} else if units == index*2+1 {
			state = 1 // half
		}
		switch {
		case index == 0:
			parts[index] = syncSegment(state, syncStartEmpty, syncStartHalf, syncStartFull)
		case index == syncProgressSegments-1:
			parts[index] = syncSegment(state, syncEndEmpty, syncEndHalf, syncEndFull)
		default:
			parts[index] = syncSegment(state, syncMiddleEmpty, syncMiddleHalf, syncMiddleFull)
		}
	}
	return strings.Join(parts, "")
}

func syncSegment(state int, empty, half, full string) string {
	switch state {
	case 2:
		return full
	case 1:
		return half
	default:
		return empty
	}
}

func syncPercent(done, total int) int {
	if total <= 0 || done <= 0 {
		return 0
	}
	if done >= total {
		return 100
	}
	return int(math.Round(float64(done) / float64(total) * 100))
}

func hasVanitySource(rules []identity.VanityRule, source identity.VanitySource) bool {
	for _, rule := range rules {
		if rule.Enabled && rule.Source == source {
			return true
		}
	}
	return false
}

func finalSyncStage(stats manualSyncStats) string {
	switch {
	case stats.FatalError != "":
		return "Synchronization failed"
	case stats.Errors > 0 || stats.Warnings > 0 || stats.UnknownIdentity > 0 || stats.UnknownVanity > 0 || stats.Limited:
		return "Completed with warnings"
	default:
		return "Synchronization completed"
	}
}

func (b *Bot) manualSyncMemberLimit() int {
	return boundedMemberLimit(b.config.ManualSyncMaxUsers)
}

func (b *Bot) manualSyncConcurrency() int {
	workers := b.config.ManualSyncConcurrency
	if workers < 1 {
		workers = 1
	}
	if workers > maxManualSyncWorkers {
		workers = maxManualSyncWorkers
	}
	return workers
}

func (b *Bot) manualPrimaryLookupTimeout() time.Duration {
	timeout := b.requestTimeout
	if timeout <= 0 {
		timeout = maxSyncPrimaryLookupTime
	}
	if timeout > maxSyncPrimaryLookupTime {
		timeout = maxSyncPrimaryLookupTime
	}
	if timeout < time.Second {
		timeout = time.Second
	}
	return timeout
}

func sanitizeSyncMessage(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	value = strings.ReplaceAll(value, "`", "'")
	const maxRunes = 240
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return value
}

func formatSyncDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	if value < time.Second {
		return fmt.Sprintf("%dms", value.Milliseconds())
	}
	if value < time.Minute {
		return fmt.Sprintf("%.1fs", value.Seconds())
	}
	minutes := int(value / time.Minute)
	seconds := int((value % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}
