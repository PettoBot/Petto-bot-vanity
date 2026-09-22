package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PettoBot/vanity-tag-bot/internal/identity"
	"github.com/bwmarrin/discordgo"
)

const (
	syncProgressSegments    = 8
	syncProgressEditEvery   = 1250 * time.Millisecond
	maxManualSyncWorkers    = 8
	maxManualSyncQueueDepth = 256

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

type syncMemberCandidate struct {
	Member       *discordgo.Member
	PrimaryGuild *identity.PrimaryGuild
	PrimaryKnown bool
}

type rawSyncUser struct {
	ID                string           `json:"id"`
	Username          string           `json:"username"`
	GlobalName        string           `json:"global_name"`
	Avatar            string           `json:"avatar"`
	Bot               bool             `json:"bot"`
	PrimaryGuild      *rawPrimaryGuild `json:"primary_guild"`
	PrimaryGuildKnown bool             `json:"-"`
}

func (u *rawSyncUser) UnmarshalJSON(data []byte) error {
	type rawSyncUserAlias rawSyncUser
	var decoded rawSyncUserAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*u = rawSyncUser(decoded)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	raw, present := fields["primary_guild"]
	u.PrimaryGuildKnown = present
	if !present || string(raw) == "null" {
		u.PrimaryGuild = nil
		return nil
	}
	var primary rawPrimaryGuild
	if err := json.Unmarshal(raw, &primary); err != nil {
		return fmt.Errorf("decode primary_guild: %w", err)
	}
	u.PrimaryGuild = &primary
	return nil
}

type rawSyncMember struct {
	User   *rawSyncUser `json:"user"`
	Nick   string       `json:"nick"`
	Avatar string       `json:"avatar"`
	Roles  []string     `json:"roles"`
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
		respond(event, fmt.Sprintf("A %s sync is already running for this server. Wait for its progress panel to finish before starting another one.", notificationSourceName(source)), true)
		return
	}
	stats := manualSyncStats{StartedAt: time.Now()}
	stage := "Preparing synchronization…"
	if userID != "" {
		stats.Total = 1
		stage = "Loading member…"
	}
	data := &discordgo.InteractionResponseData{
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
		candidate, memberErr := b.fetchSyncMember(guildID, userID, source)
		if memberErr != nil {
			stats.Processed = 1
			stats.Errors = 1
			stats.FatalError = "Could not load the selected member: " + compactSyncError(memberErr)
			b.editManualSync(interaction, source, "Synchronization failed", stats, 1, true)
			return
		}
		if candidate.Member == nil || candidate.Member.User == nil || candidate.Member.User.Bot {
			stats.Processed = 1
			stats.SkippedBots = 1
			b.editManualSync(interaction, source, "No eligible member to synchronize", stats, 1, true)
			return
		}
		result := b.syncCandidateWithSnapshot(guildID, candidate, source, vanityRules, tagRules)
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
	candidates, err := b.fetchSyncMembersPaginated(context.Background(), guildID, source, fetchLimit)
	if err != nil {
		stats.FatalError = "Could not fetch guild members: " + compactSyncError(err)
		stats.Errors++
		b.editManualSync(interaction, source, "Synchronization failed", stats, workers, true)
		return
	}

	stats.Limited = len(candidates) > limit || (limit == maxGuildSyncMembers && len(candidates) >= limit)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	humans := make([]syncMemberCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		member := candidate.Member
		if member == nil || member.User == nil || member.User.Bot {
			stats.SkippedBots++
			continue
		}
		humans = append(humans, candidate)
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
	jobs := make(chan syncMemberCandidate, queueDepth)
	results := make(chan syncMemberResult, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for candidate := range jobs {
				results <- b.syncCandidateWithSnapshot(guildID, candidate, source, vanityRules, tagRules)
			}
		}()
	}
	go func() {
		for _, candidate := range humans {
			jobs <- candidate
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

func (b *Bot) fetchSyncMember(guildID, userID string, source identity.Source) (syncMemberCandidate, error) {
	ctx, cancel := b.operationContext()
	defer cancel()
	if source != identity.SourceGuildTag {
		member, err := b.session.GuildMember(guildID, userID, discordgo.WithContext(ctx))
		if err != nil {
			return syncMemberCandidate{}, err
		}
		return syncMemberCandidate{Member: member}, nil
	}
	endpoint := discordgo.EndpointGuildMember(guildID, userID)
	raw, err := b.session.RequestWithBucketID(http.MethodGet, endpoint, nil, discordgo.EndpointGuildMember(guildID, ""), discordgo.WithContext(ctx))
	if err != nil {
		return syncMemberCandidate{}, err
	}
	var item rawSyncMember
	if err := json.Unmarshal(raw, &item); err != nil {
		return syncMemberCandidate{}, fmt.Errorf("decode guild member: %w", err)
	}
	return syncCandidateFromRaw(guildID, item), nil
}

func (b *Bot) fetchSyncMembersPaginated(parent context.Context, guildID string, source identity.Source, limit int) ([]syncMemberCandidate, error) {
	limit = boundedMemberLimit(limit)
	if source != identity.SourceGuildTag {
		members, err := b.fetchGuildMembersPaginated(parent, guildID, limit)
		if err != nil {
			return nil, err
		}
		result := make([]syncMemberCandidate, 0, len(members))
		for _, member := range members {
			result = append(result, syncMemberCandidate{Member: member})
		}
		return result, nil
	}

	result := make([]syncMemberCandidate, 0, limit)
	after := ""
	for len(result) < limit {
		pageSize := discordMemberPageSize
		if remaining := limit - len(result); remaining < pageSize {
			pageSize = remaining
		}
		requestCtx, cancel := context.WithTimeout(parent, b.requestTimeout)
		values := url.Values{}
		values.Set("limit", strconv.Itoa(pageSize))
		if after != "" {
			values.Set("after", after)
		}
		baseEndpoint := discordgo.EndpointGuildMembers(guildID)
		endpoint := baseEndpoint + "?" + values.Encode()
		raw, err := b.session.RequestWithBucketID(http.MethodGet, endpoint, nil, baseEndpoint, discordgo.WithContext(requestCtx))
		cancel()
		if err != nil {
			return result, err
		}
		var page []rawSyncMember
		if err := json.Unmarshal(raw, &page); err != nil {
			return result, fmt.Errorf("decode guild members page: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			result = append(result, syncCandidateFromRaw(guildID, item))
		}
		if len(page) < pageSize {
			break
		}
		last := page[len(page)-1]
		if last.User == nil || last.User.ID == "" || last.User.ID == after {
			break
		}
		after = last.User.ID
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func syncCandidateFromRaw(guildID string, item rawSyncMember) syncMemberCandidate {
	candidate := syncMemberCandidate{}
	if item.User == nil {
		return candidate
	}
	candidate.Member = &discordgo.Member{
		GuildID: guildID,
		Nick:    item.Nick,
		Avatar:  item.Avatar,
		Roles:   append([]string(nil), item.Roles...),
		User: &discordgo.User{
			ID:         item.User.ID,
			Username:   item.User.Username,
			GlobalName: item.User.GlobalName,
			Avatar:     item.User.Avatar,
			Bot:        item.User.Bot,
		},
	}
	candidate.PrimaryKnown = item.User.PrimaryGuildKnown || item.User.PrimaryGuild != nil
	if item.User.PrimaryGuild != nil {
		candidate.PrimaryGuild = &identity.PrimaryGuild{
			IdentityGuildID: item.User.PrimaryGuild.IdentityGuildID,
			IdentityEnabled: item.User.PrimaryGuild.IdentityEnabled,
			Tag:             item.User.PrimaryGuild.Tag,
			Badge:           item.User.PrimaryGuild.Badge,
		}
	}
	return candidate
}

func (b *Bot) loadMemberForSource(guildID, userID string, source identity.Source) (identity.MemberIdentity, bool, error) {
	candidate, err := b.fetchSyncMember(guildID, userID, source)
	if err != nil {
		return identity.MemberIdentity{}, false, err
	}
	if candidate.Member == nil {
		return identity.MemberIdentity{}, false, fmt.Errorf("member is unavailable")
	}
	resolved := b.cachedMemberIdentity(guildID, candidate.Member, candidate.PrimaryGuild)
	if source == identity.SourceGuildTag {
		return resolved, candidate.PrimaryKnown, nil
	}
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

func (b *Bot) syncCandidateWithSnapshot(guildID string, candidate syncMemberCandidate, source identity.Source, vanityRules []identity.VanityRule, tagRules []identity.GuildTagRule) syncMemberResult {
	member := candidate.Member
	if member == nil || member.User == nil || member.User.Bot {
		return syncMemberResult{}
	}
	resolved := b.cachedMemberIdentity(guildID, member, candidate.PrimaryGuild)
	unknownVanity := source == identity.SourceVanity && hasVanitySource(vanityRules, identity.VanityCustomStatus) && !identity.VanitySourceKnown(resolved, identity.VanityCustomStatus)
	if source == identity.SourceGuildTag && len(tagRules) > 0 && !candidate.PrimaryKnown {
		// Missing primary_guild is unknown data, not a failed sync. Preserve all
		// existing Guild Tag grants and do not count it as a warning/error.
		return syncMemberResult{unknownIdentity: true}
	}

	result := syncMemberResult{evaluated: true, unknownVanity: unknownVanity}
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
		result.warning = compactSyncError(evaluateErr)
		return result
	}

	result.roleDecisions = len(evaluations)
	for _, evaluation := range evaluations {
		if evaluation.Error != nil {
			result.errors++
			result.warning = compactSyncError(evaluation.Error)
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
		case stats.Warnings > 0 || stats.Limited:
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

	elapsed := syncElapsed(stats)
	if final {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Completed in", Value: "`" + formatSyncDuration(elapsed) + "`", Inline: true})
	} else if eta, rate, ok := syncEstimate(stats, elapsed); ok {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Estimated time", Value: fmt.Sprintf("`~%s` remaining · `%.1f` members/s", formatSyncDuration(eta), rate), Inline: true})
	}

	if source == identity.SourceGuildTag {
		known := stats.Evaluated
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   "Identity coverage",
			Value:  fmt.Sprintf("`%d` resolved · `%d` unknown\nResolved includes explicit `primary_guild: null` (no Server Tag). Only omitted data is preserved as unknown.", known, stats.UnknownIdentity),
			Inline: false,
		})
	}
	if source == identity.SourceVanity && stats.UnknownVanity > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Vanity coverage", Value: fmt.Sprintf("`%d` members had no cached Custom Status · those grants were preserved", stats.UnknownVanity), Inline: false})
	}
	if stats.ManualProtected > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Manual roles protected", Value: fmt.Sprintf("`%d` protected role states were left untouched", stats.ManualProtected), Inline: false})
	}
	if stats.SkippedBots > 0 {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Ignored", Value: fmt.Sprintf("`%d` bot/invalid members", stats.SkippedBots), Inline: true})
	}
	if stats.Warnings > 0 || stats.Errors > 0 || stats.Limited || stats.FatalError != "" {
		parts := make([]string, 0, 4)
		if stats.Warnings > 0 || stats.Errors > 0 {
			parts = append(parts, fmt.Sprintf("`%d` warnings · `%d` errors", stats.Warnings, stats.Errors))
		}
		if stats.Limited {
			if stats.Limit >= maxGuildSyncMembers {
				parts = append(parts, fmt.Sprintf("Safety cap reached at `%d` members.", maxGuildSyncMembers))
			} else {
				parts = append(parts, "Member limit reached; raise `MANUAL_SYNC_MAX_USERS` if you want a larger run.")
			}
		}
		if stats.FatalError != "" {
			parts = append(parts, "**Error:** "+compactSyncText(stats.FatalError))
		} else if stats.LastWarning != "" && (stats.Warnings > 0 || stats.Errors > 0) {
			parts = append(parts, "Last issue: `"+compactSyncText(stats.LastWarning)+"`")
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Issues", Value: strings.Join(parts, "\n"), Inline: false})
	}

	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Fields:      fields,
		Footer:      &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Petto sync · %d worker(s)", workers)},
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
	case stats.Errors > 0 || stats.Warnings > 0 || stats.Limited:
		return "Completed with warnings"
	case stats.UnknownIdentity > 0 || stats.UnknownVanity > 0:
		return "Synchronization completed safely"
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

func syncElapsed(stats manualSyncStats) time.Duration {
	if stats.StartedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(stats.StartedAt)
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func syncEstimate(stats manualSyncStats, elapsed time.Duration) (time.Duration, float64, bool) {
	if stats.Total <= 0 || stats.Processed <= 0 || stats.Processed >= stats.Total || elapsed < time.Second {
		return 0, 0, false
	}
	rate := float64(stats.Processed) / elapsed.Seconds()
	if rate <= 0 {
		return 0, 0, false
	}
	remaining := stats.Total - stats.Processed
	etaSeconds := float64(remaining) / rate
	if etaSeconds < 0 {
		etaSeconds = 0
	}
	return time.Duration(etaSeconds * float64(time.Second)), rate, true
}

func compactSyncError(err error) string {
	if err == nil {
		return "Unknown error"
	}
	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "context deadline exceeded") || strings.Contains(value, "timeout"):
		return "Discord request timed out. Try the sync again in a moment."
	case strings.Contains(value, "429") || strings.Contains(value, "rate limit"):
		return "Discord rate limited the request. Try the sync again shortly."
	case strings.Contains(value, "missing permissions") || strings.Contains(value, "missing permission"):
		return "Missing Discord permissions for this action."
	default:
		return compactSyncText(err.Error())
	}
}

func compactSyncText(value string) string {
	value = sanitizeSyncMessage(value)
	const maxRunes = 150
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return value
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
